package router

import (
	"strings"

	"violet-dns/cache"
)

// Matcher 域名匹配器（从 CategoryCache 查询）
type Matcher struct {
	categoryCache cache.CategoryCache
}

// NewMatcher 创建新的匹配器
func NewMatcher(categoryCache cache.CategoryCache) *Matcher {
	return &Matcher{
		categoryCache: categoryCache,
	}
}

// Match 匹配域名
// 返回匹配的分组和是否匹配成功
// 支持逐级向上查找: www.google.com -> google.com -> com
func (m *Matcher) Match(domain string) (string, bool) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	if domain == "" {
		return "", false
	}

	// 1. 先查询完整域名 (如 www.google.com)
	if group, err := m.categoryCache.Get(domain); err == nil && group != "" {
		return group, true
	}

	// 2. 逐级向上查找父域名
	parts := strings.Split(domain, ".")
	for i := 1; i < len(parts); i++ {
		parentDomain := strings.Join(parts[i:], ".")
		if group, err := m.categoryCache.Get(parentDomain); err == nil && group != "" {
			return group, true
		}
	}

	// 3. 未匹配
	return "", false
}

// MatchExact 精确匹配域名（不支持父域名查找）
func (m *Matcher) MatchExact(domain string) (string, bool) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	if domain == "" {
		return "", false
	}

	group, err := m.categoryCache.Get(domain)
	if err != nil || group == "" {
		return "", false
	}

	return group, true
}
