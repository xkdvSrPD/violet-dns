package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/miekg/dns"
	"github.com/redis/go-redis/v9"
)

// RedisDNSCache Redis DNS 缓存
type RedisDNSCache struct {
	client *redis.Client
	maxTTL time.Duration
}

// NewRedisDNSCache 创建新的 Redis DNS 缓存
func NewRedisDNSCache(client *redis.Client, maxTTL time.Duration) *RedisDNSCache {
	return &RedisDNSCache{
		client: client,
		maxTTL: maxTTL,
	}
}

// Get 获取缓存
func (c *RedisDNSCache) Get(key string) (*dns.Msg, bool) {
	ctx := context.Background()

	data, err := c.client.Get(ctx, "dns:"+key).Bytes()
	if err != nil {
		return nil, false
	}

	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, false
	}

	// 严格检查 TTL
	if time.Now().After(entry.ExpireTime) {
		// 已过期，删除
		c.client.Del(ctx, "dns:"+key)
		return nil, false
	}

	return entry.Response, true
}

// Set 设置缓存
func (c *RedisDNSCache) Set(key string, msg *dns.Msg, ttl time.Duration) error {
	ctx := context.Background()

	// 限制最大 TTL
	if ttl > c.maxTTL {
		ttl = c.maxTTL
	}

	entry := &CacheEntry{
		Response:   msg,
		ExpireTime: time.Now().Add(ttl),
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("序列化缓存失败: %w", err)
	}

	// 设置 Redis 过期时间，严格遵守 TTL
	if err := c.client.Set(ctx, "dns:"+key, data, ttl).Err(); err != nil {
		return fmt.Errorf("写入 Redis 失败: %w", err)
	}

	return nil
}

// Delete 删除缓存
func (c *RedisDNSCache) Delete(key string) error {
	ctx := context.Background()
	return c.client.Del(ctx, "dns:"+key).Err()
}

// Clear 清空缓存
func (c *RedisDNSCache) Clear() error {
	ctx := context.Background()

	// 删除所有 dns: 前缀的键
	iter := c.client.Scan(ctx, 0, "dns:*", 0).Iterator()
	for iter.Next(ctx) {
		if err := c.client.Del(ctx, iter.Val()).Err(); err != nil {
			return err
		}
	}

	return iter.Err()
}
