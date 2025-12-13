package upstream

import (
	"context"
	"fmt"
	"time"

	"violet-dns/middleware"
	"violet-dns/outbound"

	"github.com/miekg/dns"
)

// Group 上游 DNS 组
type Group struct {
	name        string
	nameservers []string
	outbound    outbound.Outbound
	strategy    string
	timeout     time.Duration
	enableECS   bool
	forceECS    bool
	ecsIP       string
	logger      *middleware.Logger
}

// NewGroup 创建新的上游组
func NewGroup(name string, nameservers []string, ob outbound.Outbound, strategy string, timeout time.Duration, logger *middleware.Logger) *Group {
	return &Group{
		name:        name,
		nameservers: nameservers,
		outbound:    ob,
		strategy:    strategy,
		timeout:     timeout,
		logger:      logger,
	}
}

// Query 查询 DNS
func (g *Group) Query(ctx context.Context, domain string, qtype uint16) (*dns.Msg, error) {
	startTime := time.Now()

	// 创建查询消息
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), qtype)
	m.RecursionDesired = true

	// 添加 ECS
	if g.enableECS && g.ecsIP != "" {
		// 简化实现：实际应该添加 EDNS0_SUBNET
		g.logger.Debug("添加ECS: domain=%s ecs_ip=%s", domain, g.ecsIP)
	}

	// 并发查询所有 nameserver
	type result struct {
		resp       *dns.Msg
		err        error
		nameserver string
		latency    time.Duration
	}

	resChan := make(chan result, len(g.nameservers))
	queryCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	for _, ns := range g.nameservers {
		go func(nameserver string) {
			queryStart := time.Now()
			resp, err := g.queryNameserver(queryCtx, m, nameserver)
			queryLatency := time.Since(queryStart)

			if err != nil {
				g.logger.Debug("Nameserver查询失败: nameserver=%s domain=%s error=%v latency=%v",
					nameserver, domain, err, queryLatency)
			}

			resChan <- result{
				resp:       resp,
				err:        err,
				nameserver: nameserver,
				latency:    queryLatency,
			}
		}(ns)
	}

	// 等待第一个成功的响应
	var lastErr error
	for i := 0; i < len(g.nameservers); i++ {
		select {
		case res := <-resChan:
			if res.err == nil && res.resp != nil {
				// DEBUG: 记录成功的响应
				g.logger.LogUpstreamResponse(domain, qtype, res.nameserver, uint16(res.resp.Rcode), len(res.resp.Answer), res.latency)
				g.logger.Debug("使用Nameserver响应: nameserver=%s group=%s total_latency=%v",
					res.nameserver, g.name, time.Since(startTime))
				return res.resp, nil
			}
			lastErr = res.err
		case <-queryCtx.Done():
			g.logger.Debug("上游查询超时: group=%s domain=%s timeout=%v", g.name, domain, g.timeout)
			return nil, fmt.Errorf("查询超时")
		}
	}

	g.logger.Debug("所有Nameserver查询失败: group=%s domain=%s last_error=%v", g.name, domain, lastErr)
	return nil, fmt.Errorf("所有 nameserver 查询失败: %v", lastErr)
}

// queryNameserver 查询单个 nameserver
func (g *Group) queryNameserver(ctx context.Context, m *dns.Msg, nameserver string) (*dns.Msg, error) {
	client := &dns.Client{
		Timeout: g.timeout,
	}

	// 简化实现：实际应该根据 nameserver 的格式选择协议
	// 如果是 https:// 开头则使用 DoH
	// 否则使用 UDP

	resp, _, err := client.ExchangeContext(ctx, m, nameserver+":53")
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// SetECS 设置 ECS
func (g *Group) SetECS(enable, force bool, ecsIP string) {
	g.enableECS = enable
	g.forceECS = force
	g.ecsIP = ecsIP
}
