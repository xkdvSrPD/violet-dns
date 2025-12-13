package upstream

import (
	"context"
	"fmt"
	"time"

	"github.com/miekg/dns"
	"violet-dns/outbound"
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
}

// NewGroup 创建新的上游组
func NewGroup(name string, nameservers []string, ob outbound.Outbound, strategy string, timeout time.Duration) *Group {
	return &Group{
		name:        name,
		nameservers: nameservers,
		outbound:    ob,
		strategy:    strategy,
		timeout:     timeout,
	}
}

// Query 查询 DNS
func (g *Group) Query(ctx context.Context, domain string, qtype uint16) (*dns.Msg, error) {
	// 创建查询消息
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), qtype)
	m.RecursionDesired = true

	// 添加 ECS
	if g.enableECS && g.ecsIP != "" {
		// 简化实现：实际应该添加 EDNS0_SUBNET
	}

	// 并发查询所有 nameserver
	type result struct {
		resp *dns.Msg
		err  error
	}

	resChan := make(chan result, len(g.nameservers))
	queryCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	for _, ns := range g.nameservers {
		go func(nameserver string) {
			resp, err := g.queryNameserver(queryCtx, m, nameserver)
			resChan <- result{resp: resp, err: err}
		}(ns)
	}

	// 等待第一个成功的响应
	for i := 0; i < len(g.nameservers); i++ {
		select {
		case res := <-resChan:
			if res.err == nil && res.resp != nil {
				return res.resp, nil
			}
		case <-queryCtx.Done():
			return nil, fmt.Errorf("查询超时")
		}
	}

	return nil, fmt.Errorf("所有 nameserver 查询失败")
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
