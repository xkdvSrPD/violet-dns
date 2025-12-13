package router

import (
	"strings"
)

// Matcher 域名匹配器
type Matcher struct {
	// 简化实现：使用 map 存储域名分组
	// 实际应该使用 Trie 树
	domainMap map[string]string // domain -> group
}

// NewMatcher 创建新的匹配器
func NewMatcher() *Matcher {
	return &Matcher{
		domainMap: make(map[string]string),
	}
}

// AddDomain 添加域名到分组
func (m *Matcher) AddDomain(domain, group string) {
	m.domainMap[domain] = group
}

// AddDomains 批量添加域名
func (m *Matcher) AddDomains(domains []string, group string) {
	for _, domain := range domains {
		m.domainMap[domain] = group
	}
}

// Match 匹配域名
func (m *Matcher) Match(domain string) (string, bool) {
	// 移除末尾的点
	domain = strings.TrimSuffix(domain, ".")

	// 精确匹配
	if group, exists := m.domainMap[domain]; exists {
		return group, true
	}

	// 尝试匹配上级域名
	parts := strings.Split(domain, ".")
	for i := 1; i < len(parts); i++ {
		parentDomain := strings.Join(parts[i:], ".")
		if group, exists := m.domainMap[parentDomain]; exists {
			return group, true
		}
	}

	return "", false
}
