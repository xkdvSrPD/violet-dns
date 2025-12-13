package cache

import (
	"context"
	"fmt"
	"sync"
	"time"

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

	// 分批写入，每批最多 100 条（减小批次以避免 broken pipe）
	const batchSize = 100
	batch := make(map[string]string, batchSize)
	count := 0
	totalWritten := 0
	totalItems := len(data)

	fmt.Printf("开始批量写入 %d 条域名分类到 Redis (每批 %d 条)...\n", totalItems, batchSize)

	for domain, category := range data {
		batch[domain] = category
		count++

		// 当批次达到大小时执行写入
		if count >= batchSize {
			if err := c.executeBatchWithRetry(ctx, batch, 3); err != nil {
				return fmt.Errorf("批量写入失败 (已写入 %d/%d 条): %w", totalWritten, totalItems, err)
			}
			totalWritten += len(batch)

			// 每 10 批显示一次进度
			if totalWritten%1000 == 0 || totalWritten == totalItems {
				fmt.Printf("进度: %d/%d (%.1f%%)\n", totalWritten, totalItems, float64(totalWritten)/float64(totalItems)*100)
			}

			// 重置批次
			batch = make(map[string]string, batchSize)
			count = 0

			// 添加小延迟避免过快发送导致连接问题
			time.Sleep(10 * time.Millisecond)
		}
	}

	// 写入剩余的数据
	if len(batch) > 0 {
		if err := c.executeBatchWithRetry(ctx, batch, 3); err != nil {
			return fmt.Errorf("批量写入失败 (最后一批): %w", err)
		}
		totalWritten += len(batch)
		fmt.Printf("进度: %d/%d (100.0%%)\n", totalWritten, totalItems)
	}

	fmt.Printf("批量写入完成，共写入 %d 条域名分类\n", totalWritten)
	return nil
}

// executeBatchWithRetry 执行单批次写入（带重试）
func (c *RedisCategoryCache) executeBatchWithRetry(ctx context.Context, batch map[string]string, maxRetries int) error {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		if err := c.executeBatch(ctx, batch); err != nil {
			lastErr = err
			// 如果失败，等待后重试
			if i < maxRetries-1 {
				waitTime := time.Duration(i+1) * 100 * time.Millisecond
				fmt.Printf("批次写入失败 (第 %d/%d 次尝试): %v，%v 后重试...\n", i+1, maxRetries, err, waitTime)
				time.Sleep(waitTime)

				// 测试连接是否还活着
				if err := c.client.Ping(ctx).Err(); err != nil {
					fmt.Printf("Redis 连接已断开: %v\n", err)
					return fmt.Errorf("Redis 连接断开: %w", err)
				}
			}
			continue
		}
		// 成功
		return nil
	}

	return fmt.Errorf("重试 %d 次后仍然失败: %w", maxRetries, lastErr)
}

// executeBatch 执行单批次写入
func (c *RedisCategoryCache) executeBatch(ctx context.Context, batch map[string]string) error {
	// 使用 MSET 命令而不是 Pipeline，更稳定
	// MSET 是原子操作，一次设置多个键值对
	args := make([]interface{}, 0, len(batch)*2)
	for domain, category := range batch {
		args = append(args, "category:"+domain, category)
	}

	return c.client.MSet(ctx, args...).Err()
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
