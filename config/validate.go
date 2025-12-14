package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Validate 验证配置
func Validate(cfg *Config) error {
	// 验证端口
	if err := validatePort(cfg.Server.Port); err != nil {
		return fmt.Errorf("server.port: %w", err)
	}

	// 验证 Bootstrap
	if err := validateBootstrap(&cfg.Bootstrap); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	// 验证 Upstream Group
	if err := validateUpstreamGroup(cfg.UpstreamGroup); err != nil {
		return fmt.Errorf("upstream_group: %w", err)
	}

	// 验证 Outbound
	if err := validateOutbound(cfg.Outbound, cfg.UpstreamGroup); err != nil {
		return fmt.Errorf("outbound: %w", err)
	}

	// 验证 ECS
	if err := validateECS(&cfg.ECS); err != nil {
		return fmt.Errorf("ecs: %w", err)
	}

	// 验证 Cache
	if err := validateCache(&cfg.Cache, &cfg.Redis); err != nil {
		return fmt.Errorf("cache: %w", err)
	}

	// 验证 Category Policy
	if err := validateCategoryPolicy(&cfg.CategoryPolicy); err != nil {
		return fmt.Errorf("category_policy: %w", err)
	}

	// 验证 Query Policy
	if err := validateQueryPolicy(cfg.QueryPolicy, cfg.CategoryPolicy.Preload.DomainGroup, cfg.UpstreamGroup); err != nil {
		return fmt.Errorf("query_policy: %w", err)
	}

	// 验证 Fallback
	if err := validateFallback(&cfg.Fallback); err != nil {
		return fmt.Errorf("fallback: %w", err)
	}

	// 验证 Performance
	if err := validatePerformance(&cfg.Performance); err != nil {
		return fmt.Errorf("performance: %w", err)
	}

	// 验证 Log
	if err := validateLog(&cfg.Log); err != nil {
		return fmt.Errorf("log: %w", err)
	}

	return nil
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口号必须在 1-65535 范围内，当前为: %d", port)
	}
	return nil
}

func validateBootstrap(cfg *BootstrapConfig) error {
	if len(cfg.Nameservers) == 0 {
		return fmt.Errorf("至少需要配置一个 nameserver")
	}
	return nil
}

func validateUpstreamGroup(groups map[string]*UpstreamGroupConfig) error {
	// 必须有三个组
	requiredGroups := []string{"proxy", "proxy_ecs", "direct"}
	for _, name := range requiredGroups {
		group, exists := groups[name]
		if !exists {
			return fmt.Errorf("缺少必需的组: %s", name)
		}
		if len(group.Nameservers) == 0 {
			return fmt.Errorf("组 %s 至少需要一个 nameserver", name)
		}
	}
	return nil
}

func validateOutbound(outbounds []OutboundConfig, groups map[string]*UpstreamGroupConfig) error {
	// 收集所有 outbound tag 和类型
	outboundTags := make(map[string]bool)
	outboundTypes := make(map[string]string)
	outboundTags["direct"] = true      // direct 默认存在
	outboundTypes["direct"] = "direct" // direct 类型

	for _, ob := range outbounds {
		if ob.Type == "socks5" {
			if ob.Server == "" || ob.Port == 0 {
				return fmt.Errorf("socks5 outbound %s 必须配置 server 和 port", ob.Tag)
			}
			if err := validatePort(ob.Port); err != nil {
				return fmt.Errorf("outbound %s: %w", ob.Tag, err)
			}
		}
		outboundTags[ob.Tag] = true
		outboundTypes[ob.Tag] = ob.Type
	}

	// 验证 upstream_group 引用的 outbound 存在
	for name, group := range groups {
		if group.Outbound != "" && !outboundTags[group.Outbound] {
			return fmt.Errorf("组 %s 引用的 outbound 不存在: %s", name, group.Outbound)
		}

		// 验证非 direct outbound 的 nameserver 必须是 HTTPS
		if group.Outbound != "direct" && group.Outbound != "" {
			outboundType := outboundTypes[group.Outbound]
			if outboundType != "direct" {
				// 检查所有 nameserver 是否都是 https://
				for _, ns := range group.Nameservers {
					if !strings.HasPrefix(ns, "https://") {
						return fmt.Errorf("组 %s 使用非 direct outbound (%s)，nameserver 必须使用 HTTPS 协议，当前为: %s", name, group.Outbound, ns)
					}
				}
			}
		}
	}
	return nil
}

func validateECS(cfg *ECSConfig) error {
	if !cfg.Enable {
		return nil
	}

	// 验证 IPv4
	if cfg.DefaultIPv4 != "" {
		if _, _, err := net.ParseCIDR(cfg.DefaultIPv4); err != nil {
			return fmt.Errorf("default_ipv4 格式无效: %w", err)
		}
	}

	// 验证 IPv6
	if cfg.DefaultIPv6 != "" {
		if _, _, err := net.ParseCIDR(cfg.DefaultIPv6); err != nil {
			return fmt.Errorf("default_ipv6 格式无效: %w", err)
		}
	}

	// 验证前缀长度
	if cfg.IPv4Prefix < 8 || cfg.IPv4Prefix > 32 {
		return fmt.Errorf("ipv4_prefix 必须在 8-32 范围内")
	}
	if cfg.IPv6Prefix < 32 || cfg.IPv6Prefix > 128 {
		return fmt.Errorf("ipv6_prefix 必须在 32-128 范围内")
	}

	return nil
}

func validateCache(cache *CacheConfig, redis *RedisConfig) error {
	// 验证 DNS Cache
	if cache.DNSCache.Enable {
		validTypes := map[string]bool{"redis": true, "memory": true}
		if !validTypes[cache.DNSCache.Type] {
			return fmt.Errorf("dns_cache.type 必须是 redis 或 memory")
		}

		if cache.DNSCache.Type == "redis" && redis.Server == "" {
			return fmt.Errorf("dns_cache.type 为 redis 时必须配置 redis 连接信息")
		}
	}

	// 验证 Category Cache
	if cache.CategoryCache.Enable {
		validTypes := map[string]bool{"redis": true, "memory": true}
		if !validTypes[cache.CategoryCache.Type] {
			return fmt.Errorf("category_cache.type 必须是 redis 或 memory")
		}
	}

	return nil
}

func validateCategoryPolicy(cfg *CategoryPolicyConfig) error {
	if !cfg.Preload.Enable {
		return nil
	}

	if cfg.Preload.File == "" {
		return fmt.Errorf("preload 启用时必须配置 file")
	}

	return nil
}

func validateQueryPolicy(policies []QueryPolicyConfig, domainGroups map[string][]string, groups map[string]*UpstreamGroupConfig) error {
	for i, policy := range policies {
		// 验证名称匹配
		if policy.Name != "unknown" {
			if _, exists := domainGroups[policy.Name]; !exists {
				return fmt.Errorf("策略 %d: 名称 %s 不存在于 domain_group 中", i, policy.Name)
			}
		}

		// 验证 group 存在
		if policy.Group != "block" && policy.Group != "proxy_ecs_fallback" {
			if _, exists := groups[policy.Group]; !exists {
				return fmt.Errorf("策略 %s: group %s 不存在于 upstream_group 中", policy.Name, policy.Group)
			}
		}

		// 验证 block_type
		if policy.Group == "block" {
			validBlockTypes := map[string]bool{"nxdomain": true, "noerror": true, "0.0.0.0": true}
			if policy.Options.BlockType != "" && !validBlockTypes[policy.Options.BlockType] {
				return fmt.Errorf("策略 %s: block_type 无效", policy.Name)
			}
		}

		// 验证 fallback_group 存在
		if policy.Options.FallbackGroup != "" {
			if _, exists := groups[policy.Options.FallbackGroup]; !exists {
				return fmt.Errorf("策略 %s: fallback_group %s 不存在", policy.Name, policy.Options.FallbackGroup)
			}
		}

		// 验证 expected_ips 格式
		for _, rule := range policy.Options.ExpectedIPs {
			if !strings.HasPrefix(rule, "geoip:") && !strings.HasPrefix(rule, "asn:") {
				return fmt.Errorf("策略 %s: expected_ips 规则格式无效: %s", policy.Name, rule)
			}
		}
	}

	return nil
}

func validateFallback(cfg *FallbackConfig) error {
	if cfg.GeoIP == "" {
		return fmt.Errorf("必须配置 geoip")
	}
	if cfg.ASN == "" {
		return fmt.Errorf("必须配置 asn")
	}
	if len(cfg.Rule) == 0 {
		return fmt.Errorf("必须至少配置一条 rule")
	}

	// 验证 rule 格式
	for _, rule := range cfg.Rule {
		if !strings.HasPrefix(rule, "geoip:") && !strings.HasPrefix(rule, "asn:") {
			return fmt.Errorf("rule 格式无效: %s", rule)
		}
		if strings.HasPrefix(rule, "asn:") {
			asnStr := strings.TrimPrefix(rule, "asn:")
			if _, err := strconv.Atoi(asnStr); err != nil {
				return fmt.Errorf("ASN 号码格式无效: %s", rule)
			}
		}
	}

	return nil
}

func validatePerformance(cfg *PerformanceConfig) error {
	if cfg.MaxConcurrentQueries <= 0 {
		return fmt.Errorf("max_concurrent_queries 必须大于 0")
	}
	return nil
}

func validateLog(cfg *LogConfig) error {
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[cfg.Level] {
		return fmt.Errorf("level 必须是 debug, info, warn 或 error")
	}

	validFormats := map[string]bool{"json": true, "text": true}
	if !validFormats[cfg.Format] {
		return fmt.Errorf("format 必须是 json 或 text")
	}

	return nil
}
