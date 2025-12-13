package router

import (
	"strings"
)

// TrieNode Trie 树节点
type TrieNode struct {
	children map[string]*TrieNode
	group    string // 如果非空，表示这是一个终止节点
	isEnd    bool   // 是否是域名终点
}

// NewTrieNode 创建新的 Trie 节点
func NewTrieNode() *TrieNode {
	return &TrieNode{
		children: make(map[string]*TrieNode),
	}
}

// Matcher 域名匹配器（使用 Trie 树）
type Matcher struct {
	root *TrieNode
}

// NewMatcher 创建新的匹配器
func NewMatcher() *Matcher {
	return &Matcher{
		root: NewTrieNode(),
	}
}

// AddDomain 添加域名到分组
// 域名以反向顺序存储在 Trie 树中
// 例如: "www.google.com" 存储为 com -> google -> www
func (m *Matcher) AddDomain(domain, group string) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	if domain == "" {
		return
	}

	// 将域名按点分割并反转
	parts := strings.Split(domain, ".")
	reverse(parts)

	// 在 Trie 树中插入
	node := m.root
	for _, part := range parts {
		if _, exists := node.children[part]; !exists {
			node.children[part] = NewTrieNode()
		}
		node = node.children[part]
	}

	// 标记终止节点
	node.isEnd = true
	node.group = group
}

// AddDomains 批量添加域名
func (m *Matcher) AddDomains(domains []string, group string) {
	for _, domain := range domains {
		m.AddDomain(domain, group)
	}
}

// Match 匹配域名
// 返回匹配的分组和是否匹配成功
func (m *Matcher) Match(domain string) (string, bool) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	if domain == "" {
		return "", false
	}

	// 将域名按点分割并反转
	parts := strings.Split(domain, ".")
	reverse(parts)

	// 在 Trie 树中查找，支持部分匹配
	// 例如: "www.google.com" 可以匹配 "google.com" 或 "com"
	node := m.root
	lastMatch := ""

	for _, part := range parts {
		child, exists := node.children[part]
		if !exists {
			break
		}

		node = child

		// 如果当前节点是终止节点，记录匹配
		if node.isEnd {
			lastMatch = node.group
		}
	}

	if lastMatch != "" {
		return lastMatch, true
	}

	return "", false
}

// MatchExact 精确匹配域名（不支持部分匹配）
func (m *Matcher) MatchExact(domain string) (string, bool) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	if domain == "" {
		return "", false
	}

	parts := strings.Split(domain, ".")
	reverse(parts)

	node := m.root
	for _, part := range parts {
		child, exists := node.children[part]
		if !exists {
			return "", false
		}
		node = child
	}

	if node.isEnd {
		return node.group, true
	}

	return "", false
}

// reverse 反转字符串切片
func reverse(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// Size 返回 Trie 树中的域名数量
func (m *Matcher) Size() int {
	return m.countNodes(m.root)
}

// countNodes 递归计算终止节点数量
func (m *Matcher) countNodes(node *TrieNode) int {
	count := 0
	if node.isEnd {
		count = 1
	}

	for _, child := range node.children {
		count += m.countNodes(child)
	}

	return count
}
