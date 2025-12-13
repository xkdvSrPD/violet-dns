package cache

import (
	"fmt"
	"time"

	"github.com/miekg/dns"
)

// DNSCache DNS 缓存接口
type DNSCache interface {
	Get(key string) (*dns.Msg, bool)
	Set(key string, msg *dns.Msg, ttl time.Duration) error
	Delete(key string) error
	Clear() error
}

// CacheEntry 缓存条目
type CacheEntry struct {
	Response   *dns.Msg
	ExpireTime time.Time
}

// IsExpired 检查是否过期
func (e *CacheEntry) IsExpired() bool {
	return time.Now().After(e.ExpireTime)
}

// GenerateCacheKey 生成缓存键
func GenerateCacheKey(domain string, qtype uint16) string {
	return fmt.Sprintf("%s:%d", domain, qtype)
}
