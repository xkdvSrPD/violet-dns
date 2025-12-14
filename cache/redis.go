package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/miekg/dns"
	"github.com/redis/go-redis/v9"
)

// RedisDNSCache Redis DNS 缓存（RR 级别）
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

// GetRRs 获取 RR 记录
func (c *RedisDNSCache) GetRRs(qname string, qtype uint16) ([]*RRCacheItem, bool) {
	ctx := context.Background()
	key := "dns:" + CacheKey{
		Name:  dns.Fqdn(qname),
		Type:  qtype,
		Class: dns.ClassINET,
	}.String()

	// 获取所有成员（RR 记录）
	members, err := c.client.ZRangeWithScores(ctx, key, 0, -1).Result()
	if err != nil || len(members) == 0 {
		return nil, false
	}

	now := time.Now().UTC()
	items := make([]*RRCacheItem, 0, len(members))

	for _, member := range members {
		data := []byte(member.Member.(string))
		expireNano := int64(member.Score)
		expireTime := time.Unix(0, expireNano)

		// 检查是否过期
		if now.After(expireTime) {
			// 过期，从 Redis 中删除
			c.client.ZRem(ctx, key, member.Member)
			continue
		}

		// 解析 RR 记录
		item, err := c.decodeRRCacheItem(data, expireTime)
		if err != nil {
			// 解析失败，删除
			c.client.ZRem(ctx, key, member.Member)
			continue
		}

		items = append(items, item)
	}

	if len(items) == 0 {
		// 所有记录都过期，删除整个 key
		c.client.Del(ctx, key)
		return nil, false
	}

	return items, true
}

// SetRRs 缓存多条 RR 记录
func (c *RedisDNSCache) SetRRs(qname string, qtype uint16, items []*RRCacheItem) error {
	if len(items) == 0 {
		return nil
	}

	ctx := context.Background()
	key := "dns:" + CacheKey{
		Name:  dns.Fqdn(qname),
		Type:  qtype,
		Class: dns.ClassINET,
	}.String()

	now := time.Now().UTC()

	// 使用 pipeline 批量写入
	pipe := c.client.Pipeline()

	// CRITICAL: 先删除旧记录,避免重复累积
	// 使用 DEL 而非 ZREM,因为我们要清空整个 key 后重新写入
	pipe.Del(ctx, key)

	var maxExpire time.Duration

	for _, item := range items {
		// 限制最大 TTL
		ttl := time.Duration(item.OrigTTL) * time.Second
		if ttl > c.maxTTL {
			ttl = c.maxTTL
			item.OrigTTL = uint32(c.maxTTL.Seconds())
		}

		item.StoredAt = now
		expireTime := now.Add(ttl)

		if ttl > maxExpire {
			maxExpire = ttl
		}

		// 编码 RR 记录
		data, err := c.encodeRRCacheItem(item)
		if err != nil {
			return fmt.Errorf("编码RR失败: %w", err)
		}

		// 使用 ZADD 添加到有序集合，score 为过期时间（纳秒）
		pipe.ZAdd(ctx, key, redis.Z{
			Score:  float64(expireTime.UnixNano()),
			Member: data,
		})
	}

	// 设置整个 key 的过期时间（使用最大 TTL + 余量）
	pipe.Expire(ctx, key, maxExpire+time.Hour)

	_, err := pipe.Exec(ctx)
	return err
}

// SetSingleRR 缓存单条 RR 记录
func (c *RedisDNSCache) SetSingleRR(item *RRCacheItem) error {
	hdr := item.RR.Header()
	return c.SetRRs(hdr.Name, hdr.Rrtype, []*RRCacheItem{item})
}

// DeleteRRs 删除指定 qname 和 qtype 的所有 RR 记录
func (c *RedisDNSCache) DeleteRRs(qname string, qtype uint16) error {
	ctx := context.Background()
	key := "dns:" + CacheKey{
		Name:  dns.Fqdn(qname),
		Type:  qtype,
		Class: dns.ClassINET,
	}.String()

	return c.client.Del(ctx, key).Err()
}

// Clear 清空所有 DNS 缓存
func (c *RedisDNSCache) Clear() error {
	ctx := context.Background()

	iter := c.client.Scan(ctx, 0, "dns:*", 0).Iterator()
	for iter.Next(ctx) {
		if err := c.client.Del(ctx, iter.Val()).Err(); err != nil {
			return err
		}
	}

	return iter.Err()
}

// rrCacheJSON RR 缓存项的 JSON 表示（可读格式）
type rrCacheJSON struct {
	RRString   string `json:"rr"`          // RR 的文本表示（如 "example.com. 300 IN A 1.2.3.4"）
	RRType     string `json:"type"`        // 记录类型（如 "A", "AAAA", "CNAME"）
	OrigTTL    uint32 `json:"orig_ttl"`    // 原始 TTL（秒）
	StoredAt   string `json:"stored_at"`   // 缓存时间（RFC3339 格式）
	Rcode      string `json:"rcode"`       // 响应码（如 "NOERROR", "NXDOMAIN"）
	AuthData   bool   `json:"auth_data"`   // AD 位
	RecurAvail bool   `json:"recur_avail"` // RA 位
}

// encodeRRCacheItem 编码 RR 缓存项为 JSON 格式
func (c *RedisDNSCache) encodeRRCacheItem(item *RRCacheItem) ([]byte, error) {
	jsonItem := rrCacheJSON{
		RRString:   item.RR.String(),
		RRType:     dns.TypeToString[item.RR.Header().Rrtype],
		OrigTTL:    item.OrigTTL,
		StoredAt:   item.StoredAt.Format(time.RFC3339Nano),
		Rcode:      dns.RcodeToString[item.Rcode],
		AuthData:   item.AuthData,
		RecurAvail: item.RecurAvail,
	}

	return json.Marshal(jsonItem)
}

// decodeRRCacheItem 从 JSON 解码 RR 缓存项
func (c *RedisDNSCache) decodeRRCacheItem(data []byte, expireTime time.Time) (*RRCacheItem, error) {
	var jsonItem rrCacheJSON
	if err := json.Unmarshal(data, &jsonItem); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}

	// 解析 RR 字符串
	rr, err := dns.NewRR(jsonItem.RRString)
	if err != nil {
		return nil, fmt.Errorf("解析 RR 字符串失败: %w", err)
	}

	// 解析存储时间
	storedAt, err := time.Parse(time.RFC3339Nano, jsonItem.StoredAt)
	if err != nil {
		return nil, fmt.Errorf("解析时间失败: %w", err)
	}

	// 解析 Rcode
	rcode, ok := dns.StringToRcode[jsonItem.Rcode]
	if !ok {
		return nil, fmt.Errorf("未知的 Rcode: %s", jsonItem.Rcode)
	}

	return &RRCacheItem{
		RR:         rr,
		OrigTTL:    jsonItem.OrigTTL,
		StoredAt:   storedAt,
		Rcode:      rcode,
		AuthData:   jsonItem.AuthData,
		RecurAvail: jsonItem.RecurAvail,
	}, nil
}
