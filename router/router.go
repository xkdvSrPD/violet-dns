package router

import (
	"context"
	"fmt"
	"time"

	"violet-dns/cache"
	"violet-dns/geoip"
	"violet-dns/middleware"
	"violet-dns/upstream"
	"violet-dns/utils"

	"github.com/miekg/dns"
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
	fallbackRules []string      // Fallback 规则
	fallbackTTL   time.Duration // 域名分类缓存 TTL
}

// NewRouter 创建新的路由器
func NewRouter(
	upstreamMgr *upstream.Manager,
	geoipMatcher *geoip.Matcher,
	dnsCache cache.DNSCache,
	categoryCache cache.CategoryCache,
	logger *middleware.Logger,
	fallbackRules []string,
	fallbackTTL time.Duration,
) *Router {
	return &Router{
		matcher:       NewMatcher(),
		policies:      make([]*Policy, 0),
		upstreamMgr:   upstreamMgr,
		geoipMatcher:  geoipMatcher,
		dnsCache:      dnsCache,
		categoryCache: categoryCache,
		logger:        logger,
		fallbackRules: fallbackRules,
		fallbackTTL:   fallbackTTL,
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

	// DEBUG: 记录查询开始
	r.logger.LogQueryStart(ctx, "", domain, qtype) // clientIP在上层已记录

	// 1. 检查缓存
	cacheKey := cache.GenerateCacheKey(domain, qtype)
	if cachedResp, hit := r.dnsCache.Get(cacheKey); hit {
		latency := time.Since(startTime)
		// DEBUG: 缓存命中
		r.logger.LogCacheHit(ctx, domain, qtype, time.Duration(cachedResp.Answer[0].Header().Ttl)*time.Second)
		// INFO: 记录查询完成
		r.logger.LogQueryComplete(ctx, domain, qtype, uint16(cachedResp.Rcode), true, latency, "cache", len(cachedResp.Answer))
		return cachedResp, nil
	}

	// DEBUG: 缓存未命中
	r.logger.LogCacheMiss(ctx, domain, qtype)

	// 2. 匹配域名分组
	groupName, matched := r.matcher.Match(domain)
	if !matched {
		groupName = "unknown"
	}

	// DEBUG: 记录分类匹配
	r.logger.LogCategoryMatch(ctx, domain, groupName, matched)

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
		r.logger.Debug("使用默认策略: domain=%s policy=unknown group=proxy_ecs_fallback", domain)
	}

	// DEBUG: 记录策略匹配
	r.logger.LogPolicyMatch(ctx, domain, policy.Name, policy.Group)

	// DEBUG: 记录策略选项（如果有）
	if len(policy.Options.ExpectedIPs) > 0 || policy.Options.FallbackGroup != "" || policy.Options.DisableCache || policy.Options.RewriteTTL > 0 {
		options := make(map[string]interface{})
		if len(policy.Options.ExpectedIPs) > 0 {
			options["expected_ips"] = policy.Options.ExpectedIPs
		}
		if policy.Options.FallbackGroup != "" {
			options["fallback_group"] = policy.Options.FallbackGroup
		}
		if policy.Options.DisableCache {
			options["disable_cache"] = true
		}
		if policy.Options.RewriteTTL > 0 {
			options["rewrite_ttl"] = policy.Options.RewriteTTL
		}
		r.logger.LogPolicyOptions(ctx, domain, options)
	}

	// 4. 处理 block 策略
	if policy.Group == "block" {
		r.logger.LogBlock(ctx, domain, qtype, policy.Options.BlockType)
		return r.handleBlock(ctx, domain, qtype, policy.Options.BlockType)
	}

	// 5. 处理 proxy_ecs_fallback 策略
	if policy.Group == "proxy_ecs_fallback" {
		r.logger.Debug("执行 proxy_ecs_fallback 策略: domain=%s", domain)
		resp, err := r.handleProxyECSFallback(ctx, domain, qtype)
		if err == nil {
			// 缓存 proxy_ecs_fallback 结果
			if !policy.Options.DisableCache {
				ttl := r.getTTL(resp)
				if policy.Options.RewriteTTL > 0 {
					ttl = time.Duration(policy.Options.RewriteTTL) * time.Second
				}
				r.dnsCache.Set(cacheKey, resp, ttl)
				r.logger.LogCacheSet(ctx, domain, qtype, ttl)
			}

			latency := time.Since(startTime)
			r.logger.LogQueryComplete(ctx, domain, qtype, uint16(resp.Rcode), false, latency, "proxy_ecs_fallback", len(resp.Answer))
		}
		return resp, err
	}

	// 6. 普通查询
	r.logger.Debug("执行普通查询: domain=%s group=%s", domain, policy.Group)
	resp, err := r.upstreamMgr.Query(ctx, policy.Group, domain, qtype)
	if err != nil {
		r.logger.LogError(ctx, "上游查询失败", domain, err, map[string]interface{}{
			"upstream_group": policy.Group,
		})
		return nil, err
	}

	// DEBUG: 记录 DNS 应答
	r.logger.LogDNSAnswer(ctx, domain, resp.Answer)

	// 7. 验证 expected_ips
	if len(policy.Options.ExpectedIPs) > 0 {
		ips := utils.ExtractIPs(resp.Answer)
		validated := r.validateIPs(resp, policy.Options.ExpectedIPs)

		// 将 IP 转换为字符串用于日志
		ipStrs := make([]string, len(ips))
		for i, ip := range ips {
			ipStrs[i] = ip.String()
		}

		// DEBUG: 记录 IP 验证
		r.logger.LogIPValidation(ctx, domain, ipStrs, policy.Options.ExpectedIPs, validated)

		if !validated {
			// IP 不符合预期
			if policy.Options.FallbackGroup != "" {
				// 使用 fallback_group 重新查询
				r.logger.LogFallback(ctx, domain, policy.Group, policy.Options.FallbackGroup, "IP不符合expected_ips")
				r.logger.LogFallbackDetail(ctx, domain, policy.Group, policy.Options.FallbackGroup, "IP不符合expected_ips", map[string]interface{}{
					"actual_ips":   ipStrs,
					"expected_ips": policy.Options.ExpectedIPs,
				})

				resp, err = r.upstreamMgr.Query(ctx, policy.Options.FallbackGroup, domain, qtype)
				if err != nil {
					r.logger.LogError(ctx, "Fallback查询失败", domain, err, map[string]interface{}{
						"fallback_group": policy.Options.FallbackGroup,
					})
					return nil, err
				}

				// DEBUG: 记录 fallback 后的应答
				r.logger.LogDNSAnswer(ctx, domain, resp.Answer)

				// fallback 查询成功，跳出到缓存逻辑（不再重复缓存代码）
			} else {
				// 继续匹配下一个策略
				r.logger.Debug("IP验证失败且无fallback_group，回退到unknown策略: domain=%s", domain)
				resp, err := r.handleProxyECSFallback(ctx, domain, qtype)
				if err == nil {
					// 缓存 proxy_ecs_fallback 结果
					if !policy.Options.DisableCache {
						ttl := r.getTTL(resp)
						if policy.Options.RewriteTTL > 0 {
							ttl = time.Duration(policy.Options.RewriteTTL) * time.Second
						}
						r.dnsCache.Set(cacheKey, resp, ttl)
						r.logger.LogCacheSet(ctx, domain, qtype, ttl)
					}

					latency := time.Since(startTime)
					r.logger.LogQueryComplete(ctx, domain, qtype, uint16(resp.Rcode), false, latency, "proxy_ecs_fallback", len(resp.Answer))
				}
				return resp, err
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

		// DEBUG: 记录缓存写入
		r.logger.LogCacheSet(ctx, domain, qtype, ttl)
	}

	latency := time.Since(startTime)
	// INFO: 记录查询完成
	r.logger.LogQueryComplete(ctx, domain, qtype, uint16(resp.Rcode), false, latency, policy.Group, len(resp.Answer))

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
	// DEBUG: 记录开始执行 proxy_ecs_fallback
	r.logger.LogProxyECSFallback(ctx, domain, "开始并发查询", map[string]interface{}{
		"groups": []string{"proxy_ecs", "proxy"},
	})

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
				if res.err != nil {
					r.logger.LogProxyECSFallback(ctx, domain, "proxy_ecs查询失败", map[string]interface{}{
						"error": res.err.Error(),
					})
				} else {
					r.logger.LogProxyECSFallback(ctx, domain, "proxy_ecs查询成功", map[string]interface{}{
						"answer_count": len(proxyECSResp.Answer),
					})
				}
			} else {
				proxyResp = res.resp
				if res.err != nil {
					r.logger.LogProxyECSFallback(ctx, domain, "proxy查询失败", map[string]interface{}{
						"error": res.err.Error(),
					})
				} else {
					r.logger.LogProxyECSFallback(ctx, domain, "proxy查询成功", map[string]interface{}{
						"answer_count": len(proxyResp.Answer),
					})
				}
			}
		case <-timeout:
			r.logger.LogProxyECSFallback(ctx, domain, "查询超时", map[string]interface{}{
				"timeout": "3s",
			})
			break
		}
	}

	// 如果 proxy_ecs 返回且 IP 匹配规则，使用 direct
	if proxyECSResp != nil {
		ips := utils.ExtractIPs(proxyECSResp.Answer)

		// 将 IP 转换为字符串用于日志
		ipStrs := make([]string, len(ips))
		for i, ip := range ips {
			ipStrs[i] = ip.String()
		}

		r.logger.LogProxyECSFallback(ctx, domain, "检查proxy_ecs结果IP", map[string]interface{}{
			"ips": ipStrs,
		})

		// 使用配置的 fallback 规则进行匹配
		for _, ip := range ips {
			// 检查 IP 是否匹配任一规则
			if r.geoipMatcher.MatchAny(ip, r.fallbackRules) {
				r.logger.LogFallback(ctx, domain, "proxy_ecs", "direct", "IP匹配fallback规则")
				r.logger.LogProxyECSFallback(ctx, domain, "IP匹配规则，回退到direct", map[string]interface{}{
					"ip":    ip.String(),
					"rules": r.fallbackRules,
				})

				// 使用 direct 查询
				directResp, err := r.upstreamMgr.Query(ctx, "direct", domain, qtype)
				if err == nil {
					// 异步写入域名分类缓存 (分类为 direct_site)
					go r.asyncCacheCategory(domain, "direct_site")
				}
				return directResp, err
			}
		}

		r.logger.LogProxyECSFallback(ctx, domain, "IP未匹配fallback规则", map[string]interface{}{
			"ips":   ipStrs,
			"rules": r.fallbackRules,
		})
	}

	// 使用 proxy 结果
	if proxyResp != nil {
		r.logger.LogProxyECSFallback(ctx, domain, "使用proxy结果", map[string]interface{}{
			"answer_count": len(proxyResp.Answer),
		})
		// 异步写入域名分类缓存 (分类为 proxy_site)
		go r.asyncCacheCategory(domain, "proxy_site")
		return proxyResp, nil
	}

	// 都失败了，返回 proxy_ecs 结果
	if proxyECSResp != nil {
		r.logger.LogProxyECSFallback(ctx, domain, "使用proxy_ecs结果（proxy失败）", map[string]interface{}{
			"answer_count": len(proxyECSResp.Answer),
		})
		// 异步写入域名分类缓存 (分类为 proxy_site，因为无法确定)
		go r.asyncCacheCategory(domain, "proxy_site")
		return proxyECSResp, nil
	}

	r.logger.LogError(ctx, "ProxyECSFallback全部失败", domain, fmt.Errorf("所有查询失败"), map[string]interface{}{})
	return nil, fmt.Errorf("所有查询失败")
}

// asyncCacheCategory 异步写入域名分类缓存
func (r *Router) asyncCacheCategory(domain, category string) {
	// 不阻塞主流程，异步写入
	if r.categoryCache != nil {
		err := r.categoryCache.Set(domain, category)
		if err != nil {
			r.logger.Debug("写入域名分类缓存失败: domain=%s category=%s error=%v", domain, category, err)
		} else {
			r.logger.Debug("写入域名分类缓存成功: domain=%s category=%s", domain, category)
		}
	}
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
