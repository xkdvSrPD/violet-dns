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

// Router 查询路由器（支持 RR 级别缓存）
type Router struct {
	matcher       *Matcher
	policies      []*Policy
	upstreamMgr   *upstream.Manager
	geoipMatcher  *geoip.Matcher
	dnsCache      cache.DNSCache // 使用新的 RR 级别缓存
	categoryCache cache.CategoryCache
	logger        *middleware.Logger
	fallbackRules []string // Fallback 规则
}

// NewRouter 创建新的路由器
func NewRouter(
	upstreamMgr *upstream.Manager,
	geoipMatcher *geoip.Matcher,
	dnsCache cache.DNSCache,
	categoryCache cache.CategoryCache,
	logger *middleware.Logger,
	fallbackRules []string,
) *Router {
	return &Router{
		matcher:       NewMatcher(categoryCache), // 传入 categoryCache
		policies:      make([]*Policy, 0),
		upstreamMgr:   upstreamMgr,
		geoipMatcher:  geoipMatcher,
		dnsCache:      dnsCache,
		categoryCache: categoryCache,
		logger:        logger,
		fallbackRules: fallbackRules,
	}
}

// AddPolicy 添加策略
func (r *Router) AddPolicy(policy *Policy) {
	r.policies = append(r.policies, policy)
}

// Route 路由查询（支持 CNAME 链部分缓存）
func (r *Router) Route(ctx context.Context, domain string, qtype uint16) (*dns.Msg, error) {
	startTime := time.Now()

	// DEBUG: 记录查询开始
	r.logger.LogQueryStart(ctx, "", domain, qtype)

	// 1. 尝试从缓存解析 CNAME 链
	cachedAnswers, needUpstream, targetName := cache.ResolveCNAMEChain(r.dnsCache, domain, qtype, 10)

	if !needUpstream {
		// 完全命中缓存
		msg := cache.BuildResponseFromCache(domain, qtype, nil)
		msg.Answer = cachedAnswers
		latency := time.Since(startTime)

		r.logger.LogCacheHit(ctx, domain, qtype, time.Duration(cachedAnswers[0].Header().Ttl)*time.Second)
		r.logger.LogQueryComplete(ctx, domain, qtype, uint16(msg.Rcode), true, latency, "cache", len(msg.Answer))
		return msg, nil
	}

	// 2. 部分缓存命中或完全未命中
	if len(cachedAnswers) > 0 {
		r.logger.Debug("CNAME链部分缓存命中: domain=%s cached_depth=%d target=%s",
			domain, len(cachedAnswers), targetName)
	} else {
		r.logger.LogCacheMiss(ctx, domain, qtype)
		targetName = domain // 完全未命中，从原始域名开始查询
	}

	// 3. 匹配域名分组（使用原始查询域名，不是 CNAME 目标）
	groupName, matched := r.matcher.Match(domain)
	if !matched {
		groupName = "unknown"
	}

	r.logger.LogCategoryMatch(ctx, domain, groupName, matched)

	// 4. 查找对应的策略
	policy := r.findPolicy(groupName)

	r.logger.LogPolicyMatch(ctx, domain, policy.Name, policy.Group)

	// 记录策略选项
	if len(policy.Options.ExpectedIPs) > 0 || policy.Options.FallbackGroup != "" || policy.Options.DisableCache {
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
		r.logger.LogPolicyOptions(ctx, domain, options)
	}

	// 5. 处理 block 策略
	if policy.Group == "block" {
		r.logger.LogBlock(ctx, domain, qtype, policy.Options.BlockType)
		return r.handleBlock(ctx, domain, qtype, policy.Options.BlockType)
	}

	// 6. 处理 proxy_ecs_fallback 策略
	if policy.Group == "proxy_ecs_fallback" {
		return r.handleProxyECSFallbackV2(ctx, domain, qtype, cachedAnswers, policy, startTime)
	}

	// 7. 普通查询（查询 CNAME 链的目标域名）
	r.logger.Debug("执行普通查询: domain=%s target=%s group=%s", domain, targetName, policy.Group)
	resp, err := r.upstreamMgr.Query(ctx, policy.Group, targetName, qtype)
	if err != nil {
		r.logger.LogError(ctx, "上游查询失败", targetName, err, map[string]interface{}{
			"upstream_group": policy.Group,
		})
		return nil, err
	}

	// 8. 合并缓存的 CNAME 链和新查询的结果
	finalResp := r.mergeCNAMEChain(domain, qtype, cachedAnswers, resp)

	r.logger.LogDNSAnswer(ctx, domain, finalResp.Answer)

	// 9. 验证 expected_ips（如果配置了）
	if len(policy.Options.ExpectedIPs) > 0 {
		finalResp, err = r.handleIPValidation(ctx, domain, qtype, targetName, finalResp, policy, cachedAnswers)
		if err != nil {
			return nil, err
		}
	}

	// 10. 缓存结果（按 RR 记录分别缓存）
	if !policy.Options.DisableCache {
		r.cacheResponse(ctx, domain, finalResp, 0)
	}

	latency := time.Since(startTime)
	r.logger.LogQueryComplete(ctx, domain, qtype, uint16(finalResp.Rcode), false, latency, policy.Group, len(finalResp.Answer))

	return finalResp, nil
}

// findPolicy 查找策略
func (r *Router) findPolicy(groupName string) *Policy {
	for _, p := range r.policies {
		if p.Name == groupName {
			return p
		}
	}

	// 使用默认策略
	r.logger.Debug("使用默认策略: group=proxy_ecs_fallback")
	return &Policy{
		Name:  "unknown",
		Group: "proxy_ecs_fallback",
	}
}

// mergeCNAMEChain 合并缓存的 CNAME 链和新查询的结果
func (r *Router) mergeCNAMEChain(qname string, qtype uint16, cachedAnswers []dns.RR, upstreamResp *dns.Msg) *dns.Msg {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(qname), qtype)
	msg.Rcode = upstreamResp.Rcode
	msg.AuthenticatedData = upstreamResp.AuthenticatedData
	msg.RecursionAvailable = upstreamResp.RecursionAvailable
	msg.RecursionDesired = true

	// 先添加缓存的 CNAME 链
	msg.Answer = append(msg.Answer, cachedAnswers...)

	// 再添加上游返回的答案
	msg.Answer = append(msg.Answer, upstreamResp.Answer...)

	return msg
}

// handleIPValidation 处理 IP 验证和 fallback
func (r *Router) handleIPValidation(ctx context.Context, domain string, qtype uint16, targetName string,
	resp *dns.Msg, policy *Policy, cachedAnswers []dns.RR) (*dns.Msg, error) {

	ips := utils.ExtractIPs(resp.Answer)
	validated := r.validateIPs(resp, policy.Options.ExpectedIPs)

	ipStrs := make([]string, len(ips))
	for i, ip := range ips {
		ipStrs[i] = ip.String()
	}

	r.logger.LogIPValidation(ctx, domain, ipStrs, policy.Options.ExpectedIPs, validated)

	if !validated {
		if policy.Options.FallbackGroup != "" {
			// 使用 fallback_group 重新查询
			r.logger.LogFallback(ctx, domain, policy.Group, policy.Options.FallbackGroup, "IP不符合expected_ips")
			r.logger.LogFallbackDetail(ctx, domain, policy.Group, policy.Options.FallbackGroup, "IP不符合expected_ips", map[string]interface{}{
				"actual_ips":   ipStrs,
				"expected_ips": policy.Options.ExpectedIPs,
			})

			fallbackResp, err := r.upstreamMgr.Query(ctx, policy.Options.FallbackGroup, targetName, qtype)
			if err != nil {
				r.logger.LogError(ctx, "Fallback查询失败", domain, err, map[string]interface{}{
					"fallback_group": policy.Options.FallbackGroup,
				})
				return nil, err
			}

			r.logger.LogDNSAnswer(ctx, domain, fallbackResp.Answer)

			// 合并 CNAME 链
			return r.mergeCNAMEChain(domain, qtype, cachedAnswers, fallbackResp), nil
		} else {
			// 回退到 proxy_ecs_fallback
			r.logger.Debug("IP验证失败且无fallback_group，回退到unknown策略: domain=%s", domain)
			return r.handleProxyECSFallbackV2(ctx, domain, qtype, nil, &Policy{
				Name:  "unknown",
				Group: "proxy_ecs_fallback",
			}, time.Now())
		}
	}

	return resp, nil
}

// cacheResponse 缓存响应（按 RR 记录分别缓存）
func (r *Router) cacheResponse(ctx context.Context, domain string, resp *dns.Msg, rewriteTTL uint32) {
	// 按 qname+qtype 分组
	type cacheKey struct {
		name  string
		qtype uint16
	}
	grouped := make(map[cacheKey][]*cache.RRCacheItem)

	for _, rr := range resp.Answer {
		hdr := rr.Header()
		key := cacheKey{
			name:  hdr.Name,
			qtype: hdr.Rrtype,
		}

		ttl := hdr.Ttl
		if rewriteTTL > 0 {
			ttl = rewriteTTL
		}

		item := &cache.RRCacheItem{
			RR:         dns.Copy(rr),
			OrigTTL:    ttl,
			StoredAt:   time.Now().UTC(),
			Rcode:      resp.Rcode,
			AuthData:   resp.AuthenticatedData,
			RecurAvail: resp.RecursionAvailable,
		}

		grouped[key] = append(grouped[key], item)
	}

	// 批量写入缓存
	for key, items := range grouped {
		if err := r.dnsCache.SetRRs(key.name, key.qtype, items); err != nil {
			r.logger.Debug("缓存写入失败: qname=%s qtype=%d error=%v", key.name, key.qtype, err)
		} else {
			// 计算 TTL 用于日志
			var ttl time.Duration
			if len(items) > 0 {
				ttl = time.Duration(items[0].OrigTTL) * time.Second
			}
			r.logger.LogCacheSet(ctx, key.name, key.qtype, ttl)
		}
	}
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

// handleProxyECSFallbackV2 处理 proxy_ecs_fallback 策略（支持 CNAME 链）
func (r *Router) handleProxyECSFallbackV2(ctx context.Context, domain string, qtype uint16,
	cachedAnswers []dns.RR, policy *Policy, startTime time.Time) (*dns.Msg, error) {

	r.logger.LogProxyECSFallback(ctx, domain, "开始并发查询", map[string]interface{}{
		"groups": []string{"proxy_ecs", "proxy"},
	})

	type result struct {
		resp *dns.Msg
		err  error
		from string
	}

	resChan := make(chan result, 2)

	// 并发查询 proxy_ecs 和 proxy
	go func() {
		resp, err := r.upstreamMgr.Query(ctx, "proxy_ecs", domain, qtype)
		resChan <- result{resp: resp, err: err, from: "proxy_ecs"}
	}()

	go func() {
		resp, err := r.upstreamMgr.Query(ctx, "proxy", domain, qtype)
		resChan <- result{resp: resp, err: err, from: "proxy"}
	}()

	// 等待结果
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

	// 检查 proxy_ecs 结果是否匹配 fallback 规则
	if proxyECSResp != nil {
		ips := utils.ExtractIPs(proxyECSResp.Answer)
		ipStrs := make([]string, len(ips))
		for i, ip := range ips {
			ipStrs[i] = ip.String()
		}

		r.logger.LogProxyECSFallback(ctx, domain, "判断是否需要fallback到direct", map[string]interface{}{
			"ips": ipStrs,
		})

		for _, ip := range ips {
			if r.geoipMatcher.MatchAny(ip, r.fallbackRules) {
				r.logger.LogFallback(ctx, domain, "proxy_ecs", "direct", "执行fallback到direct")

				directResp, err := r.upstreamMgr.Query(ctx, "direct", domain, qtype)
				if err == nil {
					// 缓存结果
					if !policy.Options.DisableCache {
						r.cacheResponse(ctx, domain, directResp, 0)
					}

					// 异步写入域名分类缓存
					go r.asyncCacheCategory(domain, "direct_site")

					latency := time.Since(startTime)
					r.logger.LogQueryComplete(ctx, domain, qtype, uint16(directResp.Rcode), false, latency, "direct", len(directResp.Answer))
					return directResp, nil
				}
				return directResp, err
			}
		}
	}

	// 使用 proxy 结果
	if proxyResp != nil {
		// 缓存结果
		if !policy.Options.DisableCache {
			r.cacheResponse(ctx, domain, proxyResp, 0)
		}

		go r.asyncCacheCategory(domain, "proxy_site")

		latency := time.Since(startTime)
		r.logger.LogQueryComplete(ctx, domain, qtype, uint16(proxyResp.Rcode), false, latency, "proxy", len(proxyResp.Answer))
		return proxyResp, nil
	}

	// 使用 proxy_ecs 结果
	if proxyECSResp != nil {
		// 缓存结果
		if !policy.Options.DisableCache {
			r.cacheResponse(ctx, domain, proxyECSResp, 0)
		}

		go r.asyncCacheCategory(domain, "proxy_site")

		latency := time.Since(startTime)
		r.logger.LogQueryComplete(ctx, domain, qtype, uint16(proxyECSResp.Rcode), false, latency, "proxy_ecs", len(proxyECSResp.Answer))
		return proxyECSResp, nil
	}

	r.logger.LogError(ctx, "ProxyECSFallback全部失败", domain, fmt.Errorf("所有查询失败"), map[string]interface{}{})
	return nil, fmt.Errorf("所有查询失败")
}

// asyncCacheCategory 异步写入域名分类缓存
func (r *Router) asyncCacheCategory(domain, category string) {
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
