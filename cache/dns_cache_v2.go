package cache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// RRCacheItem 单条 RR 记录的缓存项
type RRCacheItem struct {
	RR         dns.RR    // DNS 资源记录
	OrigTTL    uint32    // 原始 TTL（秒）
	StoredAt   time.Time // 缓存时间（UTC）
	Rcode      int       // 响应码
	AuthData   bool      // AD 位
	RecurAvail bool      // RA 位
}

// IsExpired 检查是否过期
func (item *RRCacheItem) IsExpired(now time.Time) bool {
	elapsed := now.Sub(item.StoredAt)
	return elapsed >= time.Duration(item.OrigTTL)*time.Second
}

// RemainingTTL 计算剩余 TTL（秒）
func (item *RRCacheItem) RemainingTTL(now time.Time) int {
	elapsed := int(now.Sub(item.StoredAt).Seconds())
	remaining := int(item.OrigTTL) - elapsed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// CacheKey RR 缓存键
type CacheKey struct {
	Name  string // 规范化的域名（小写，带尾点）
	Type  uint16 // 记录类型（A, AAAA, CNAME 等）
	Class uint16 // 记录类别（通常是 IN）
}

// String 生成缓存键字符串
func (k CacheKey) String() string {
	return fmt.Sprintf("%s:%d:%d", k.Name, k.Type, k.Class)
}

// DNSCacheV2 新版 DNS 缓存接口（RR 级别）
type DNSCacheV2 interface {
	// GetRRs 获取指定 qname 和 qtype 的 RR 记录（只返回未过期的）
	GetRRs(qname string, qtype uint16) ([]*RRCacheItem, bool)

	// SetRRs 缓存多条 RR 记录（来自一次查询响应）
	SetRRs(qname string, qtype uint16, items []*RRCacheItem) error

	// SetSingleRR 缓存单条 RR 记录
	SetSingleRR(item *RRCacheItem) error

	// DeleteRRs 删除指定 qname 和 qtype 的所有 RR 记录
	DeleteRRs(qname string, qtype uint16) error

	// Clear 清空所有缓存
	Clear() error
}

// MemoryDNSCacheV2 内存 DNS 缓存（RR 级别）
type MemoryDNSCacheV2 struct {
	mu      sync.RWMutex
	storage map[string][]*RRCacheItem // key -> RR 列表
	maxTTL  time.Duration             // 最大允许 TTL
}

// NewMemoryDNSCacheV2 创建新的内存 DNS 缓存
func NewMemoryDNSCacheV2(maxTTL time.Duration) *MemoryDNSCacheV2 {
	return &MemoryDNSCacheV2{
		storage: make(map[string][]*RRCacheItem),
		maxTTL:  maxTTL,
	}
}

// GetRRs 获取 RR 记录（自动过滤过期记录）
func (c *MemoryDNSCacheV2) GetRRs(qname string, qtype uint16) ([]*RRCacheItem, bool) {
	key := CacheKey{
		Name:  dns.Fqdn(qname),
		Type:  qtype,
		Class: dns.ClassINET,
	}.String()

	c.mu.RLock()
	items, exists := c.storage[key]
	c.mu.RUnlock()

	if !exists {
		return nil, false
	}

	now := time.Now().UTC()
	validItems := make([]*RRCacheItem, 0, len(items))

	for _, item := range items {
		if !item.IsExpired(now) {
			validItems = append(validItems, item)
		}
	}

	// 如果所有记录都过期，清理缓存
	if len(validItems) == 0 {
		c.mu.Lock()
		delete(c.storage, key)
		c.mu.Unlock()
		return nil, false
	}

	// 如果有部分过期，更新缓存（移除过期项）
	if len(validItems) < len(items) {
		c.mu.Lock()
		c.storage[key] = validItems
		c.mu.Unlock()
	}

	return validItems, true
}

// SetRRs 缓存多条 RR 记录
func (c *MemoryDNSCacheV2) SetRRs(qname string, qtype uint16, items []*RRCacheItem) error {
	if len(items) == 0 {
		return nil
	}

	key := CacheKey{
		Name:  dns.Fqdn(qname),
		Type:  qtype,
		Class: dns.ClassINET,
	}.String()

	// 限制最大 TTL
	now := time.Now().UTC()
	for _, item := range items {
		if time.Duration(item.OrigTTL)*time.Second > c.maxTTL {
			item.OrigTTL = uint32(c.maxTTL.Seconds())
		}
		item.StoredAt = now
	}

	c.mu.Lock()
	c.storage[key] = items
	c.mu.Unlock()

	return nil
}

// SetSingleRR 缓存单条 RR 记录
func (c *MemoryDNSCacheV2) SetSingleRR(item *RRCacheItem) error {
	hdr := item.RR.Header()
	key := CacheKey{
		Name:  hdr.Name,
		Type:  hdr.Rrtype,
		Class: hdr.Class,
	}.String()

	// 限制最大 TTL
	if time.Duration(item.OrigTTL)*time.Second > c.maxTTL {
		item.OrigTTL = uint32(c.maxTTL.Seconds())
	}
	item.StoredAt = time.Now().UTC()

	c.mu.Lock()
	c.storage[key] = []*RRCacheItem{item}
	c.mu.Unlock()

	return nil
}

// DeleteRRs 删除指定 qname 和 qtype 的所有 RR 记录
func (c *MemoryDNSCacheV2) DeleteRRs(qname string, qtype uint16) error {
	key := CacheKey{
		Name:  dns.Fqdn(qname),
		Type:  qtype,
		Class: dns.ClassINET,
	}.String()

	c.mu.Lock()
	delete(c.storage, key)
	c.mu.Unlock()

	return nil
}

// Clear 清空所有缓存
func (c *MemoryDNSCacheV2) Clear() error {
	c.mu.Lock()
	c.storage = make(map[string][]*RRCacheItem)
	c.mu.Unlock()
	return nil
}

// ParseResponseToRRCache 将 DNS 响应解析为 RR 缓存项
func ParseResponseToRRCache(msg *dns.Msg) []*RRCacheItem {
	items := make([]*RRCacheItem, 0, len(msg.Answer))

	for _, rr := range msg.Answer {
		item := &RRCacheItem{
			RR:         dns.Copy(rr), // 深拷贝
			OrigTTL:    rr.Header().Ttl,
			StoredAt:   time.Now().UTC(),
			Rcode:      msg.Rcode,
			AuthData:   msg.AuthenticatedData,
			RecurAvail: msg.RecursionAvailable,
		}
		items = append(items, item)
	}

	return items
}

// BuildResponseFromCache 从缓存项构建 DNS 响应
func BuildResponseFromCache(qname string, qtype uint16, items []*RRCacheItem) *dns.Msg {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(qname), qtype)

	if len(items) == 0 {
		return msg
	}

	now := time.Now().UTC()

	// 使用第一条记录的元数据
	msg.Rcode = items[0].Rcode
	msg.AuthenticatedData = items[0].AuthData
	msg.RecursionAvailable = items[0].RecurAvail
	msg.RecursionDesired = true

	// 设置 Answer 记录，更新 TTL 为剩余时间
	msg.Answer = make([]dns.RR, len(items))
	for i, item := range items {
		rr := dns.Copy(item.RR)
		rr.Header().Ttl = uint32(item.RemainingTTL(now))
		msg.Answer[i] = rr
	}

	return msg
}

// ResolveCNAMEChain 解析 CNAME 链（支持部分缓存查询）
// 返回: 完整的 Answer RR 列表, 是否需要查询上游, 需要查询的目标域名
func ResolveCNAMEChain(cache DNSCacheV2, qname string, qtype uint16, maxDepth int) ([]dns.RR, bool, string) {
	if maxDepth <= 0 {
		maxDepth = 10 // 默认最大深度
	}

	answers := make([]dns.RR, 0)
	currentName := dns.Fqdn(qname)
	now := time.Now().UTC()

	for depth := 0; depth < maxDepth; depth++ {
		// 1. 尝试查询目标类型（A/AAAA）
		if items, hit := cache.GetRRs(currentName, qtype); hit {
			// 找到最终答案
			for _, item := range items {
				rr := dns.Copy(item.RR)
				rr.Header().Ttl = uint32(item.RemainingTTL(now))
				answers = append(answers, rr)
			}
			return answers, false, ""
		}

		// 2. 尝试查询 CNAME
		cnameItems, cnameHit := cache.GetRRs(currentName, dns.TypeCNAME)
		if !cnameHit {
			// CNAME 缓存未命中，需要查询上游
			return answers, true, currentName
		}

		// 添加 CNAME 记录到答案
		for _, item := range cnameItems {
			rr := dns.Copy(item.RR)
			rr.Header().Ttl = uint32(item.RemainingTTL(now))
			answers = append(answers, rr)

			// 提取 CNAME 目标
			if cnameRR, ok := rr.(*dns.CNAME); ok {
				currentName = dns.Fqdn(cnameRR.Target)
			}
		}
	}

	// 超过最大深度，返回已收集的答案并查询最后的目标
	return answers, true, currentName
}

// CacheResponseByRR 将 DNS 响应按 RR 记录分别缓存
func CacheResponseByRR(cache DNSCacheV2, msg *dns.Msg) error {
	ctx := context.Background()
	_ = ctx

	// 按 qname+qtype 分组缓存
	grouped := make(map[string][]*RRCacheItem)

	for _, rr := range msg.Answer {
		hdr := rr.Header()
		key := CacheKey{
			Name:  hdr.Name,
			Type:  hdr.Rrtype,
			Class: hdr.Class,
		}.String()

		item := &RRCacheItem{
			RR:         dns.Copy(rr),
			OrigTTL:    hdr.Ttl,
			StoredAt:   time.Now().UTC(),
			Rcode:      msg.Rcode,
			AuthData:   msg.AuthenticatedData,
			RecurAvail: msg.RecursionAvailable,
		}

		grouped[key] = append(grouped[key], item)
	}

	// 批量写入缓存
	for keyStr, items := range grouped {
		// 解析 key
		var qname string
		var qtype, qclass uint16
		fmt.Sscanf(keyStr, "%s:%d:%d", &qname, &qtype, &qclass)

		if err := cache.SetRRs(qname, qtype, items); err != nil {
			return err
		}
	}

	return nil
}
