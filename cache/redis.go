package cache

import (
	"context"
	"encoding/binary"
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

	// 数据格式: [8字节过期时间Unix纳秒][DNS消息二进制]
	if len(data) < 8 {
		// 无效数据，删除
		c.client.Del(ctx, "dns:"+key)
		return nil, false
	}

	// 解析过期时间
	expireNano := int64(binary.BigEndian.Uint64(data[:8]))
	expireTime := time.Unix(0, expireNano)

	// 严格检查 TTL
	if time.Now().After(expireTime) {
		// 已过期，删除
		c.client.Del(ctx, "dns:"+key)
		return nil, false
	}

	// 解析 DNS 消息
	msg := new(dns.Msg)
	if err := msg.Unpack(data[8:]); err != nil {
		// 解析失败，删除无效缓存
		c.client.Del(ctx, "dns:"+key)
		return nil, false
	}

	return msg, true
}

// Set 设置缓存
func (c *RedisDNSCache) Set(key string, msg *dns.Msg, ttl time.Duration) error {
	ctx := context.Background()

	// 限制最大 TTL
	if ttl > c.maxTTL {
		ttl = c.maxTTL
	}

	// 序列化 DNS 消息为二进制格式
	packed, err := msg.Pack()
	if err != nil {
		return fmt.Errorf("序列化DNS消息失败: %w", err)
	}

	// 数据格式: [8字节过期时间Unix纳秒][DNS消息二进制]
	expireTime := time.Now().Add(ttl)
	data := make([]byte, 8+len(packed))
	binary.BigEndian.PutUint64(data[:8], uint64(expireTime.UnixNano()))
	copy(data[8:], packed)

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
