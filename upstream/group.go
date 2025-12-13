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
	ecsIP       string // 有值则添加 ECS，空则不添加
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

	// 添加 ECS（仅当配置了 ecs_ip 时）
	if g.ecsIP != "" {
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

				// DEBUG: 记录详细的响应数据
				responseDetails := formatDNSResponse(res.resp)
				g.logger.Debug("收到上游返回数据: nameserver=%s group=%s total_latency=%v response=%s",
					res.nameserver, g.name, time.Since(startTime), responseDetails)

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

// formatDNSResponse 格式化 DNS 响应为可读字符串
func formatDNSResponse(msg *dns.Msg) string {
	if msg == nil {
		return "nil"
	}

	var parts []string

	// Rcode
	parts = append(parts, fmt.Sprintf("rcode=%s", dns.RcodeToString[msg.Rcode]))

	// Answer 记录
	if len(msg.Answer) > 0 {
		answers := make([]string, 0, len(msg.Answer))
		for _, rr := range msg.Answer {
			answers = append(answers, formatRR(rr))
		}
		parts = append(parts, fmt.Sprintf("answers=[%s]", strings.Join(answers, "; ")))
	} else {
		parts = append(parts, "answers=[]")
	}

	// Authority 记录
	if len(msg.Ns) > 0 {
		ns := make([]string, 0, len(msg.Ns))
		for _, rr := range msg.Ns {
			ns = append(ns, formatRR(rr))
		}
		parts = append(parts, fmt.Sprintf("authority=[%s]", strings.Join(ns, "; ")))
	}

	// Additional 记录（排除 OPT）
	if len(msg.Extra) > 0 {
		extra := make([]string, 0)
		for _, rr := range msg.Extra {
			if rr.Header().Rrtype != dns.TypeOPT {
				extra = append(extra, formatRR(rr))
			}
		}
		if len(extra) > 0 {
			parts = append(parts, fmt.Sprintf("additional=[%s]", strings.Join(extra, "; ")))
		}
	}

	return strings.Join(parts, " ")
}

// formatRR 格式化单条 RR 记录
func formatRR(rr dns.RR) string {
	hdr := rr.Header()
	rrType := dns.TypeToString[hdr.Rrtype]

	var value string
	switch rr := rr.(type) {
	case *dns.A:
		value = rr.A.String()
	case *dns.AAAA:
		value = rr.AAAA.String()
	case *dns.CNAME:
		value = rr.Target
	case *dns.MX:
		value = fmt.Sprintf("preference=%d mail=%s", rr.Preference, rr.Mx)
	case *dns.NS:
		value = rr.Ns
	case *dns.PTR:
		value = rr.Ptr
	case *dns.SOA:
		value = fmt.Sprintf("mname=%s rname=%s", rr.Ns, rr.Mbox)
	case *dns.TXT:
		value = strings.Join(rr.Txt, " ")
	case *dns.SRV:
		value = fmt.Sprintf("priority=%d weight=%d port=%d target=%s", rr.Priority, rr.Weight, rr.Port, rr.Target)
	default:
		// 其他类型，使用通用格式
		value = strings.TrimPrefix(rr.String(), hdr.String())
		value = strings.TrimSpace(value)
	}

	// 格式: [类型] 域名 (TTL=X秒) -> 值
	return fmt.Sprintf("[%s] %s (ttl=%ds) -> %s", rrType, hdr.Name, hdr.Ttl, value)
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

// SetECS 设置 ECS IP
func (g *Group) SetECS(ecsIP string) {
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
