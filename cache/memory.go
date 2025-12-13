package cache

import (
	"sync"
	"time"

	"github.com/miekg/dns"
)

// MemoryDNSCache 内存 DNS 缓存
type MemoryDNSCache struct {
	data       map[string]*CacheEntry
	mu         sync.RWMutex
	maxTTL     time.Duration
	serveStale bool
	staleTTL   time.Duration
}

// NewMemoryDNSCache 创建新的内存 DNS 缓存
func NewMemoryDNSCache(maxTTL time.Duration, serveStale bool, staleTTL time.Duration) *MemoryDNSCache {
	return &MemoryDNSCache{
		data:       make(map[string]*CacheEntry),
		maxTTL:     maxTTL,
		serveStale: serveStale,
		staleTTL:   staleTTL,
	}
}

// Get 获取缓存
func (c *MemoryDNSCache) Get(key string) (*dns.Msg, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.data[key]
	if !exists {
		return nil, false
	}

	now := time.Now()

	// 未过期，直接返回
	if now.Before(entry.ExpireTime) {
		return entry.Response.Copy(), true
	}

	// 过期但 stale 可用
	if c.serveStale && now.Before(entry.StaleUntil) {
		return entry.Response.Copy(), true
	}

	// 完全过期
	return nil, false
}

// Set 设置缓存
func (c *MemoryDNSCache) Set(key string, msg *dns.Msg, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 限制最大 TTL
	if ttl > c.maxTTL {
		ttl = c.maxTTL
	}

	now := time.Now()
	entry := &CacheEntry{
		Response:   msg.Copy(),
		ExpireTime: now.Add(ttl),
		StaleUntil: now.Add(ttl + c.staleTTL),
	}

	c.data[key] = entry
	return nil
}

// Delete 删除缓存
func (c *MemoryDNSCache) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.data, key)
	return nil
}

// Clear 清空缓存
func (c *MemoryDNSCache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data = make(map[string]*CacheEntry)
	return nil
}
