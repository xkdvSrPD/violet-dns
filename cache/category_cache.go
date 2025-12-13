package cache

import (
	"context"
	"sync"

	"github.com/redis/go-redis/v9"
)

// CategoryCache 分类缓存接口
type CategoryCache interface {
	Get(domain string) (string, error)
	Set(domain, category string) error
	BatchSet(data map[string]string) error
	Delete(domain string) error
	Clear() error
}

// MemoryCategoryCache 内存分类缓存
type MemoryCategoryCache struct {
	data map[string]string
	mu   sync.RWMutex
}

// NewMemoryCategoryCache 创建内存分类缓存
func NewMemoryCategoryCache() *MemoryCategoryCache {
	return &MemoryCategoryCache{
		data: make(map[string]string),
	}
}

// Get 获取域名分类
func (c *MemoryCategoryCache) Get(domain string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	category, exists := c.data[domain]
	if !exists {
		return "", nil
	}

	return category, nil
}

// Set 设置域名分类
func (c *MemoryCategoryCache) Set(domain, category string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data[domain] = category
	return nil
}

// BatchSet 批量设置域名分类
func (c *MemoryCategoryCache) BatchSet(data map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for domain, category := range data {
		c.data[domain] = category
	}

	return nil
}

// Delete 删除域名分类
func (c *MemoryCategoryCache) Delete(domain string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.data, domain)
	return nil
}

// Clear 清空缓存
func (c *MemoryCategoryCache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data = make(map[string]string)
	return nil
}

// RedisCategoryCache Redis 分类缓存
type RedisCategoryCache struct {
	client *redis.Client
}

// NewRedisCategoryCache 创建 Redis 分类缓存
func NewRedisCategoryCache(client *redis.Client) *RedisCategoryCache {
	return &RedisCategoryCache{
		client: client,
	}
}

// Get 获取域名分类
func (c *RedisCategoryCache) Get(domain string) (string, error) {
	ctx := context.Background()
	return c.client.Get(ctx, "category:"+domain).Result()
}

// Set 设置域名分类
func (c *RedisCategoryCache) Set(domain, category string) error {
	ctx := context.Background()
	return c.client.Set(ctx, "category:"+domain, category, 0).Err()
}

// BatchSet 批量设置域名分类
func (c *RedisCategoryCache) BatchSet(data map[string]string) error {
	ctx := context.Background()

	// 使用 Pipeline 批量写入
	pipe := c.client.Pipeline()
	for domain, category := range data {
		pipe.Set(ctx, "category:"+domain, category, 0)
	}

	_, err := pipe.Exec(ctx)
	return err
}

// Delete 删除域名分类
func (c *RedisCategoryCache) Delete(domain string) error {
	ctx := context.Background()
	return c.client.Del(ctx, "category:"+domain).Err()
}

// Clear 清空缓存
func (c *RedisCategoryCache) Clear() error {
	ctx := context.Background()

	// 删除所有 category: 前缀的键
	iter := c.client.Scan(ctx, 0, "category:*", 0).Iterator()
	for iter.Next(ctx) {
		if err := c.client.Del(ctx, iter.Val()).Err(); err != nil {
			return err
		}
	}

	return iter.Err()
}
