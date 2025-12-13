package cache

import (
	"sync"
	"time"

	"github.com/miekg/dns"
)

// MemoryDNSCache 内存 DNS 缓存
type MemoryDNSCache struct {
	data          map[string]*CacheEntry
	mu            sync.RWMutex
	maxTTL        time.Duration
	cleanupTicker *time.Ticker
	stopCleanup   chan struct{}
}

// NewMemoryDNSCache 创建新的内存 DNS 缓存
func NewMemoryDNSCache(maxTTL time.Duration) *MemoryDNSCache {
	c := &MemoryDNSCache{
		data:          make(map[string]*CacheEntry),
		maxTTL:        maxTTL,
		cleanupTicker: time.NewTicker(1 * time.Minute), // 每分钟清理一次
		stopCleanup:   make(chan struct{}),
	}

	// 启动后台清理 goroutine
	go c.cleanupExpired()

	return c
}

// cleanupExpired 定期清理过期条目
func (c *MemoryDNSCache) cleanupExpired() {
	for {
		select {
		case <-c.cleanupTicker.C:
			c.mu.Lock()
			now := time.Now()
			for key, entry := range c.data {
				// 过期则删除
				if now.After(entry.ExpireTime) {
					delete(c.data, key)
				}
			}
			c.mu.Unlock()
		case <-c.stopCleanup:
			c.cleanupTicker.Stop()
			return
		}
	}
}

// Close 关闭缓存，停止清理 goroutine
func (c *MemoryDNSCache) Close() error {
	close(c.stopCleanup)
	return nil
}

// Get 获取缓存
func (c *MemoryDNSCache) Get(key string) (*dns.Msg, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.data[key]
	if !exists {
		return nil, false
	}

	// 严格检查 TTL
	if time.Now().After(entry.ExpireTime) {
		return nil, false
	}

	return entry.Response.Copy(), true
}

// Set 设置缓存
func (c *MemoryDNSCache) Set(key string, msg *dns.Msg, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 限制最大 TTL
	if ttl > c.maxTTL {
		ttl = c.maxTTL
	}

	entry := &CacheEntry{
		Response:   msg.Copy(),
		ExpireTime: time.Now().Add(ttl),
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

// Size 返回缓存条目数量
func (c *MemoryDNSCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data)
}
