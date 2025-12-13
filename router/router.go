package router

import (
	"context"
	"fmt"
	"time"

	"github.com/miekg/dns"
	"violet-dns/cache"
	"violet-dns/geoip"
	"violet-dns/middleware"
	"violet-dns/upstream"
	"violet-dns/utils"
)

// Router 查询路由器
type Router struct {
	matcher       *Matcher
	policies      []*Policy
	upstreamMgr   *upstream.Manager
	geoipMatcher  *geoip.Matcher
	dnsCache      cache.DNSCache
	categoryCache cache.CategoryCache
	logger        *middleware.Logger
}

// NewRouter 创建新的路由器
func NewRouter(
	upstreamMgr *upstream.Manager,
	geoipMatcher *geoip.Matcher,
	dnsCache cache.DNSCache,
	categoryCache cache.CategoryCache,
	logger *middleware.Logger,
) *Router {
	return &Router{
		matcher:       NewMatcher(),
		policies:      make([]*Policy, 0),
		upstreamMgr:   upstreamMgr,
		geoipMatcher:  geoipMatcher,
		dnsCache:      dnsCache,
		categoryCache: categoryCache,
		logger:        logger,
	}
}

// AddPolicy 添加策略
func (r *Router) AddPolicy(policy *Policy) {
	r.policies = append(r.policies, policy)
}

// LoadDomainGroup 加载域名分组
func (r *Router) LoadDomainGroup(domainGroups map[string][]string) {
	for groupName, domains := range domainGroups {
		r.matcher.AddDomains(domains, groupName)
	}
}

// Route 路由查询
func (r *Router) Route(ctx context.Context, domain string, qtype uint16) (*dns.Msg, error) {
	startTime := time.Now()

	// 1. 检查缓存
	cacheKey := cache.GenerateCacheKey(domain, qtype)
	if cachedResp, hit := r.dnsCache.Get(cacheKey); hit {
		latency := time.Since(startTime)
		r.logger.LogQuery(domain, qtype, uint16(cachedResp.Rcode), true, latency, "cache")
		return cachedResp, nil
	}

	// 2. 匹配域名分组
	groupName, matched := r.matcher.Match(domain)
	if !matched {
		groupName = "unknown"
	}

	// 3. 查找对应的策略
	var policy *Policy
	for _, p := range r.policies {
		if p.Name == groupName {
			policy = p
			break
		}
	}

	if policy == nil {
		// 使用默认策略
		policy = &Policy{
			Name:  "unknown",
			Group: "proxy_ecs_fallback",
		}
	}

	// 4. 处理 block 策略
	if policy.Group == "block" {
		return r.handleBlock(ctx, domain, qtype, policy.Options.BlockType)
	}

	// 5. 处理 proxy_ecs_fallback 策略
	if policy.Group == "proxy_ecs_fallback" {
		return r.handleProxyECSFallback(ctx, domain, qtype)
	}

	// 6. 普通查询
	resp, err := r.upstreamMgr.Query(ctx, policy.Group, domain, qtype)
	if err != nil {
		return nil, err
	}

	// 7. 验证 expected_ips
	if len(policy.Options.ExpectedIPs) > 0 {
		if !r.validateIPs(resp, policy.Options.ExpectedIPs) {
			// IP 不符合预期
			if policy.Options.FallbackGroup != "" {
				// 使用 fallback_group 重新查询
				r.logger.LogFallback(domain, policy.Group, policy.Options.FallbackGroup, "IP不符合expected_ips")
				resp, err = r.upstreamMgr.Query(ctx, policy.Options.FallbackGroup, domain, qtype)
				if err != nil {
					return nil, err
				}
			} else {
				// 继续匹配下一个策略
				// 简化实现：直接使用 unknown 策略
				return r.handleProxyECSFallback(ctx, domain, qtype)
			}
		}
	}

	// 8. 缓存结果
	if !policy.Options.DisableCache {
		ttl := r.getTTL(resp)
		if policy.Options.RewriteTTL > 0 {
			ttl = time.Duration(policy.Options.RewriteTTL) * time.Second
		}
		r.dnsCache.Set(cacheKey, resp, ttl)
	}

	latency := time.Since(startTime)
	r.logger.LogQuery(domain, qtype, uint16(resp.Rcode), false, latency, policy.Group)

	return resp, nil
}

// handleBlock 处理阻止策略
func (r *Router) handleBlock(ctx context.Context, domain string, qtype uint16, blockType string) (*dns.Msg, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), qtype)

	switch blockType {
	case "nxdomain":
		return utils.CreateNXDomainResponse(m), nil
	case "noerror":
		return utils.CreateNoErrorResponse(m), nil
	case "0.0.0.0":
		return utils.CreateBlockedResponse(m), nil
	default:
		return utils.CreateNXDomainResponse(m), nil
	}
}

// handleProxyECSFallback 处理 proxy_ecs_fallback 策略
func (r *Router) handleProxyECSFallback(ctx context.Context, domain string, qtype uint16) (*dns.Msg, error) {
	// 并发查询 proxy_ecs 和 proxy
	type result struct {
		resp *dns.Msg
		err  error
		from string
	}

	resChan := make(chan result, 2)

	// 查询 proxy_ecs
	go func() {
		resp, err := r.upstreamMgr.Query(ctx, "proxy_ecs", domain, qtype)
		resChan <- result{resp: resp, err: err, from: "proxy_ecs"}
	}()

	// 查询 proxy
	go func() {
		resp, err := r.upstreamMgr.Query(ctx, "proxy", domain, qtype)
		resChan <- result{resp: resp, err: err, from: "proxy"}
	}()

	// 等待 proxy_ecs 结果
	var proxyECSResp *dns.Msg
	var proxyResp *dns.Msg

	timeout := time.After(3 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case res := <-resChan:
			if res.from == "proxy_ecs" {
				proxyECSResp = res.resp
			} else {
				proxyResp = res.resp
			}
		case <-timeout:
			break
		}
	}

	// 如果 proxy_ecs 返回且 IP 匹配规则，使用 direct
	if proxyECSResp != nil {
		ips := utils.ExtractIPs(proxyECSResp.Answer)
		for _, ip := range ips {
			// 简化：假设规则是 geoip:cn
			if r.geoipMatcher.Match(ip, "geoip:cn") {
				r.logger.LogFallback(domain, "proxy_ecs", "direct", "检测到中国IP")
				return r.upstreamMgr.Query(ctx, "direct", domain, qtype)
			}
		}
	}

	// 使用 proxy 结果
	if proxyResp != nil {
		return proxyResp, nil
	}

	// 都失败了，返回 proxy_ecs 结果
	if proxyECSResp != nil {
		return proxyECSResp, nil
	}

	return nil, fmt.Errorf("所有查询失败")
}

// validateIPs 验证 IP 是否符合预期
func (r *Router) validateIPs(resp *dns.Msg, expectedIPs []string) bool {
	ips := utils.ExtractIPs(resp.Answer)
	if len(ips) == 0 {
		return true // 没有 IP，视为通过
	}

	for _, ip := range ips {
		matched := false
		for _, rule := range expectedIPs {
			if r.geoipMatcher.Match(ip, rule) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

// getTTL 获取响应的 TTL
func (r *Router) getTTL(resp *dns.Msg) time.Duration {
	if len(resp.Answer) == 0 {
		return 300 * time.Second // 默认 5 分钟
	}

	// 使用第一个记录的 TTL
	return time.Duration(resp.Answer[0].Header().Ttl) * time.Second
}
