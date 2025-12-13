package upstream

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"violet-dns/middleware"
	"violet-dns/outbound"

	"github.com/AdguardTeam/dnsproxy/upstream"
	"github.com/miekg/dns"
)

// Group 上游 DNS 组
type Group struct {
	name        string
	nameservers []string
	upstreams   []upstream.Upstream // AdGuard 的 upstream 实例
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
	g := &Group{
		name:        name,
		nameservers: nameservers,
		outbound:    ob,
		strategy:    strategy,
		timeout:     timeout,
		logger:      logger,
		upstreams:   make([]upstream.Upstream, 0, len(nameservers)),
	}

	// 初始化所有 upstream
	for _, ns := range nameservers {
		u, err := g.createUpstream(ns)
		if err != nil {
			logger.Warn("创建 upstream 失败: nameserver=%s error=%v", ns, err)
			continue
		}
		g.upstreams = append(g.upstreams, u)
	}

	return g
}

// createUpstream 创建单个 upstream
func (g *Group) createUpstream(nameserver string) (upstream.Upstream, error) {
	// AdGuard upstream 支持的格式:
	// - https://dns.google/dns-query (DoH)
	// - tls://dns.google (DoT)
	// - quic://dns.adguard.com (DoQ)
	// - 8.8.8.8:53 或 8.8.8.8 (UDP/TCP)
	// - tcp://8.8.8.8:53 (强制 TCP)
	// - sdns://... (DNSCrypt)

	// 处理不带端口的普通 IP
	if !strings.Contains(nameserver, "://") && !strings.Contains(nameserver, ":") {
		// 检查是否是 IP 地址
		if net.ParseIP(nameserver) != nil {
			nameserver = nameserver + ":53"
		}
	}

	// 使用 AdGuard upstream 库创建
	opts := &upstream.Options{
		Timeout: g.timeout,
		// 不设置 Bootstrap，让库使用系统 DNS
	}

	u, err := upstream.AddressToUpstream(nameserver, opts)
	if err != nil {
		return nil, fmt.Errorf("创建 upstream 失败: %w", err)
	}

	return u, nil
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

	// 并发查询所有 upstream
	type result struct {
		resp       *dns.Msg
		err        error
		nameserver string
		latency    time.Duration
	}

	if len(g.upstreams) == 0 {
		return nil, fmt.Errorf("没有可用的 upstream")
	}

	resChan := make(chan result, len(g.upstreams))

	for i, u := range g.upstreams {
		go func(ups upstream.Upstream, nameserver string) {
			queryStart := time.Now()

			// 使用 upstream.Exchange 进行查询
			resp, err := ups.Exchange(m)
			queryLatency := time.Since(queryStart)

			if err != nil {
				g.logger.LogUpstreamError(ctx, domain, nameserver, err, queryLatency)
			}

			resChan <- result{
				resp:       resp,
				err:        err,
				nameserver: nameserver,
				latency:    queryLatency,
			}
		}(u, g.nameservers[i])
	}

	// 等待第一个成功的响应
	var lastErr error
	for i := 0; i < len(g.upstreams); i++ {
		select {
		case res := <-resChan:
			if res.err == nil && res.resp != nil {
				// DEBUG: 记录成功的响应
				g.logger.LogUpstreamResponse(ctx, domain, qtype, res.nameserver, uint16(res.resp.Rcode), len(res.resp.Answer), res.latency)
				g.logger.Debug("使用Nameserver响应: nameserver=%s group=%s total_latency=%v",
					res.nameserver, g.name, time.Since(startTime))
				return res.resp, nil
			}
			lastErr = res.err
		case <-ctx.Done():
			g.logger.Debug("上游查询超时: group=%s domain=%s timeout=%v", g.name, domain, g.timeout)
			return nil, fmt.Errorf("查询超时")
		}
	}

	g.logger.Debug("所有Nameserver查询失败: group=%s domain=%s last_error=%v", g.name, domain, lastErr)
	return nil, fmt.Errorf("所有 nameserver 查询失败: %v", lastErr)
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

// SetECS 设置 ECS
func (g *Group) SetECS(enable, force bool, ecsIP string) {
	g.enableECS = enable
	g.forceECS = force
	g.ecsIP = ecsIP
}

// Close 关闭所有 upstream 连接
func (g *Group) Close() error {
	for _, u := range g.upstreams {
		if closer, ok := u.(interface{ Close() error }); ok {
			closer.Close()
		}
	}
	return nil
}
