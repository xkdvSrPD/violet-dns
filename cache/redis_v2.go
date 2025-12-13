package cache

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/miekg/dns"
	"github.com/redis/go-redis/v9"
)

// RedisDNSCacheV2 Redis DNS 缓存（RR 级别）
type RedisDNSCacheV2 struct {
	client *redis.Client
	maxTTL time.Duration
}

// NewRedisDNSCacheV2 创建新的 Redis DNS 缓存
func NewRedisDNSCacheV2(client *redis.Client, maxTTL time.Duration) *RedisDNSCacheV2 {
	return &RedisDNSCacheV2{
		client: client,
		maxTTL: maxTTL,
	}
}

// GetRRs 获取 RR 记录
func (c *RedisDNSCacheV2) GetRRs(qname string, qtype uint16) ([]*RRCacheItem, bool) {
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
func (c *RedisDNSCacheV2) SetRRs(qname string, qtype uint16, items []*RRCacheItem) error {
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
func (c *RedisDNSCacheV2) SetSingleRR(item *RRCacheItem) error {
	hdr := item.RR.Header()
	return c.SetRRs(hdr.Name, hdr.Rrtype, []*RRCacheItem{item})
}

// DeleteRRs 删除指定 qname 和 qtype 的所有 RR 记录
func (c *RedisDNSCacheV2) DeleteRRs(qname string, qtype uint16) error {
	ctx := context.Background()
	key := "dns:" + CacheKey{
		Name:  dns.Fqdn(qname),
		Type:  qtype,
		Class: dns.ClassINET,
	}.String()

	return c.client.Del(ctx, key).Err()
}

// Clear 清空所有 DNS 缓存
func (c *RedisDNSCacheV2) Clear() error {
	ctx := context.Background()

	iter := c.client.Scan(ctx, 0, "dns:*", 0).Iterator()
	for iter.Next(ctx) {
		if err := c.client.Del(ctx, iter.Val()).Err(); err != nil {
			return err
		}
	}

	return iter.Err()
}

// encodeRRCacheItem 编码 RR 缓存项为二进制
// 格式: [1字节版本][4字节OrigTTL][8字节StoredAt][2字节Rcode][1字节Flags][DNS RR二进制]
func (c *RedisDNSCacheV2) encodeRRCacheItem(item *RRCacheItem) ([]byte, error) {
	// 序列化 RR
	rrData, err := packRR(item.RR)
	if err != nil {
		return nil, err
	}

	// 计算总长度
	totalLen := 1 + 4 + 8 + 2 + 1 + len(rrData)
	data := make([]byte, totalLen)

	offset := 0

	// 版本号
	data[offset] = 1
	offset++

	// OrigTTL
	binary.BigEndian.PutUint32(data[offset:], item.OrigTTL)
	offset += 4

	// StoredAt (Unix 纳秒)
	binary.BigEndian.PutUint64(data[offset:], uint64(item.StoredAt.UnixNano()))
	offset += 8

	// Rcode
	binary.BigEndian.PutUint16(data[offset:], uint16(item.Rcode))
	offset += 2

	// Flags (AuthData, RecurAvail)
	var flags byte
	if item.AuthData {
		flags |= 0x01
	}
	if item.RecurAvail {
		flags |= 0x02
	}
	data[offset] = flags
	offset++

	// RR 数据
	copy(data[offset:], rrData)

	return data, nil
}

// decodeRRCacheItem 从二进制解码 RR 缓存项
func (c *RedisDNSCacheV2) decodeRRCacheItem(data []byte, expireTime time.Time) (*RRCacheItem, error) {
	if len(data) < 16 {
		return nil, fmt.Errorf("数据太短")
	}

	offset := 0

	// 版本号
	version := data[offset]
	if version != 1 {
		return nil, fmt.Errorf("不支持的版本: %d", version)
	}
	offset++

	// OrigTTL
	origTTL := binary.BigEndian.Uint32(data[offset:])
	offset += 4

	// StoredAt
	storedNano := int64(binary.BigEndian.Uint64(data[offset:]))
	storedAt := time.Unix(0, storedNano)
	offset += 8

	// Rcode
	rcode := int(binary.BigEndian.Uint16(data[offset:]))
	offset += 2

	// Flags
	flags := data[offset]
	authData := (flags & 0x01) != 0
	recurAvail := (flags & 0x02) != 0
	offset++

	// RR 数据
	rr, err := unpackRR(data[offset:])
	if err != nil {
		return nil, fmt.Errorf("解析RR失败: %w", err)
	}

	return &RRCacheItem{
		RR:         rr,
		OrigTTL:    origTTL,
		StoredAt:   storedAt,
		Rcode:      rcode,
		AuthData:   authData,
		RecurAvail: recurAvail,
	}, nil
}

// packRR 序列化单条 RR 记录
func packRR(rr dns.RR) ([]byte, error) {
	// 创建一个临时 DNS 消息
	msg := new(dns.Msg)
	msg.Answer = []dns.RR{rr}

	packed, err := msg.Pack()
	if err != nil {
		return nil, err
	}

	// 跳过 DNS 消息头（12 字节）和 Question 段
	// 我们只需要 Answer 段的数据
	return packed[12:], nil
}

// unpackRR 反序列化单条 RR 记录
func unpackRR(data []byte) (dns.RR, error) {
	// 构造最小的 DNS 消息格式
	// Header (12字节) + Question (最小5字节) + Answer (data)
	minMsg := make([]byte, 12)

	// 设置 Header
	// ANCOUNT = 1 (有 1 条 Answer)
	binary.BigEndian.PutUint16(minMsg[6:8], 1)

	// 拼接数据
	fullData := append(minMsg, data...)

	msg := new(dns.Msg)
	if err := msg.Unpack(fullData); err != nil {
		return nil, err
	}

	if len(msg.Answer) == 0 {
		return nil, fmt.Errorf("没有Answer记录")
	}

	return msg.Answer[0], nil
}
