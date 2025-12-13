package upstream

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
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
		g.addECS(m, g.ecsIP)
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
				g.logger.LogUpstreamError(queryCtx, domain, nameserver, err, queryLatency)
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
				g.logger.LogUpstreamResponse(queryCtx, domain, qtype, res.nameserver, uint16(res.resp.Rcode), len(res.resp.Answer), res.latency)
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
	// 根据 nameserver 格式选择协议
	// 支持的格式:
	// - https://dns.google/dns-query (DoH)
	// - tls://dns.google (DoT)
	// - 8.8.8.8 或 8.8.8.8:53 (UDP/TCP)
	// - tcp://8.8.8.8:53 (强制 TCP)

	if strings.HasPrefix(nameserver, "https://") {
		return g.queryDoH(ctx, m, nameserver)
	} else if strings.HasPrefix(nameserver, "tls://") {
		return g.queryDoT(ctx, m, strings.TrimPrefix(nameserver, "tls://"))
	} else if strings.HasPrefix(nameserver, "tcp://") {
		return g.queryTCP(ctx, m, strings.TrimPrefix(nameserver, "tcp://"))
	} else {
		// 默认使用 UDP，失败时自动降级到 TCP
		return g.queryUDP(ctx, m, nameserver)
	}
}

// queryDoH 使用 DNS-over-HTTPS 查询
func (g *Group) queryDoH(ctx context.Context, m *dns.Msg, url string) (*dns.Msg, error) {
	// 使用 miekg/dns 的 DoH 客户端
	// 需要通过 outbound 连接
	client := &dns.Client{
		Net:     "https",
		Timeout: g.timeout,
	}

	// DoH 不需要端口，URL 已经包含完整地址
	resp, _, err := client.ExchangeContext(ctx, m, url)
	if err != nil {
		return nil, fmt.Errorf("DoH query failed: %w", err)
	}

	return resp, nil
}

// queryDoT 使用 DNS-over-TLS 查询
func (g *Group) queryDoT(ctx context.Context, m *dns.Msg, server string) (*dns.Msg, error) {
	// 添加默认端口
	if !strings.Contains(server, ":") {
		server = net.JoinHostPort(server, "853")
	}

	client := &dns.Client{
		Net:     "tcp-tls",
		Timeout: g.timeout,
		TLSConfig: &tls.Config{
			// 从 server 地址中提取主机名作为 ServerName
			ServerName: extractHostname(server),
		},
	}

	resp, _, err := client.ExchangeContext(ctx, m, server)
	if err != nil {
		return nil, fmt.Errorf("DoT query failed: %w", err)
	}

	return resp, nil
}

// queryTCP 使用 TCP 查询
func (g *Group) queryTCP(ctx context.Context, m *dns.Msg, server string) (*dns.Msg, error) {
	// 添加默认端口
	if !strings.Contains(server, ":") {
		server = net.JoinHostPort(server, "53")
	}

	client := &dns.Client{
		Net:     "tcp",
		Timeout: g.timeout,
	}

	resp, _, err := client.ExchangeContext(ctx, m, server)
	if err != nil {
		return nil, fmt.Errorf("TCP query failed: %w", err)
	}

	return resp, nil
}

// queryUDP 使用 UDP 查询（失败时自动降级到 TCP）
func (g *Group) queryUDP(ctx context.Context, m *dns.Msg, server string) (*dns.Msg, error) {
	// 添加默认端口
	if !strings.Contains(server, ":") {
		server = net.JoinHostPort(server, "53")
	}

	client := &dns.Client{
		Net:     "udp",
		Timeout: g.timeout,
	}

	resp, _, err := client.ExchangeContext(ctx, m, server)
	if err != nil {
		// UDP 失败，尝试 TCP
		g.logger.Debug("UDP查询失败，尝试TCP: server=%s error=%v", server, err)
		return g.queryTCP(ctx, m, server)
	}

	// 检查响应是否被截断（TC flag）
	if resp != nil && resp.Truncated {
		g.logger.Debug("UDP响应被截断，使用TCP重试: server=%s", server)
		return g.queryTCP(ctx, m, server)
	}

	return resp, nil
}

// addECS 添加 EDNS Client Subnet
func (g *Group) addECS(m *dns.Msg, ecsIP string) {
	// 解析 ECS IP（可能是 CIDR 格式）
	ip, ipNet, err := net.ParseCIDR(ecsIP)
	if err != nil {
		// 不是 CIDR 格式，尝试解析为普通 IP
		ip = net.ParseIP(ecsIP)
		if ip == nil {
			g.logger.Debug("无效的 ECS IP: %s", ecsIP)
			return
		}
		// 使用默认掩码
		if ip.To4() != nil {
			_, ipNet, _ = net.ParseCIDR(ecsIP + "/24")
		} else {
			_, ipNet, _ = net.ParseCIDR(ecsIP + "/56")
		}
	}

	// 创建 EDNS0_SUBNET 选项
	opt := new(dns.OPT)
	opt.Hdr.Name = "."
	opt.Hdr.Rrtype = dns.TypeOPT

	subnet := new(dns.EDNS0_SUBNET)
	subnet.Code = dns.EDNS0SUBNET

	if ip.To4() != nil {
		subnet.Family = 1 // IPv4
		subnet.Address = ip.To4()
	} else {
		subnet.Family = 2 // IPv6
		subnet.Address = ip.To16()
	}

	ones, _ := ipNet.Mask.Size()
	subnet.SourceNetmask = uint8(ones)
	subnet.SourceScope = 0

	opt.Option = append(opt.Option, subnet)
	m.Extra = append(m.Extra, opt)
}

// extractHostname 从地址中提取主机名
func extractHostname(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// SetECS 设置 ECS
func (g *Group) SetECS(enable, force bool, ecsIP string) {
	g.enableECS = enable
	g.forceECS = force
	g.ecsIP = ecsIP
}
