package upstream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
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
	timeout     time.Duration
	ecsIP       string // 有值则添加 ECS，空则不添加
	logger      *middleware.Logger
}

// proxyUpstream 通过 outbound 代理进行 DNS 查询的 upstream 实现
type proxyUpstream struct {
	address  string            // DNS 服务器地址 (e.g., "8.8.8.8:53" 或 "https://dns.google/dns-query")
	protocol string            // 协议: "udp", "tcp", "https"
	outbound outbound.Outbound // 出站代理
	timeout  time.Duration
}

// Exchange 实现 upstream.Upstream 接口
func (u *proxyUpstream) Exchange(m *dns.Msg) (*dns.Msg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), u.timeout)
	defer cancel()

	// 根据协议类型选择连接方式
	switch u.protocol {
	case "https":
		return u.exchangeHTTPS(ctx, m)
	case "tcp":
		return u.exchangeTCP(ctx, m)
	default:
		return nil, fmt.Errorf("不支持的协议: %s (仅支持 https 和 tcp)", u.protocol)
	}
}

// exchangeHTTPS 通过 DoH (DNS-over-HTTPS) 进行查询
func (u *proxyUpstream) exchangeHTTPS(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	// 打包 DNS 消息
	packed, err := m.Pack()
	if err != nil {
		return nil, fmt.Errorf("打包 DNS 消息失败: %w", err)
	}

	// 创建自定义 HTTP Transport，使用 outbound 代理
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// 使用 outbound 的 Dial 方法建立连接
			return u.outbound.Dial(ctx, network, addr)
		},
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   u.timeout,
	}
	defer client.CloseIdleConnections()

	// 发送 POST 请求
	req, err := http.NewRequestWithContext(ctx, "POST", u.address, bytes.NewReader(packed))
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")

	// 发送请求
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送 DoH 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH 服务器返回错误: %d %s", resp.StatusCode, resp.Status)
	}

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 DoH 响应失败: %w", err)
	}

	// 解析 DNS 响应
	respMsg := new(dns.Msg)
	if err := respMsg.Unpack(body); err != nil {
		return nil, fmt.Errorf("解析 DNS 响应失败: %w", err)
	}

	return respMsg, nil
}

// exchangeTCP 通过 TCP 进行 DNS 查询
func (u *proxyUpstream) exchangeTCP(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	// 使用 outbound 建立 TCP 连接
	conn, err := u.outbound.Dial(ctx, "tcp", u.address)
	if err != nil {
		return nil, fmt.Errorf("代理连接失败: %w", err)
	}
	defer conn.Close()

	// 创建 DNS 连接
	dnsConn := &dns.Conn{Conn: conn}

	// 发送查询
	if err := dnsConn.WriteMsg(m); err != nil {
		return nil, fmt.Errorf("发送 DNS 查询失败: %w", err)
	}

	// 接收响应
	resp, err := dnsConn.ReadMsg()
	if err != nil {
		return nil, fmt.Errorf("读取 DNS 响应失败: %w", err)
	}

	return resp, nil
}

// Address 实现 upstream.Upstream 接口
func (u *proxyUpstream) Address() string {
	return u.address
}

// Close 实现 upstream.Upstream 接口
func (u *proxyUpstream) Close() error {
	return nil
}

// NewGroup 创建新的上游组
func NewGroup(name string, nameservers []string, ob outbound.Outbound, timeout time.Duration, logger *middleware.Logger) *Group {
	g := &Group{
		name:        name,
		nameservers: nameservers,
		outbound:    ob,
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
	// 判断是否需要使用代理
	needsProxy := g.needsProxy()

	// 解析 nameserver 格式
	protocol, address := g.parseNameserver(nameserver)

	// 如果需要代理
	if needsProxy {
		// 对于所有协议（包括加密协议），都使用我们的 proxyUpstream
		g.logger.Debug("创建代理 upstream: nameserver=%s protocol=%s address=%s", nameserver, protocol, address)

		return &proxyUpstream{
			address:  address,
			protocol: protocol,
			outbound: g.outbound,
			timeout:  g.timeout,
		}, nil
	}

	// 不需要代理，使用 AdGuard upstream
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

// needsProxy 判断当前 outbound 是否需要代理
func (g *Group) needsProxy() bool {
	// 检查 outbound 是否是 DirectOutbound
	_, isDirect := g.outbound.(*outbound.DirectOutbound)
	return !isDirect
}

// parseNameserver 解析 nameserver 格式，返回协议和地址
func (g *Group) parseNameserver(nameserver string) (protocol, address string) {
	// 支持的格式:
	// - https://dns.google/dns-query (DoH)
	// - tls://dns.google (DoT) - 不支持代理
	// - quic://dns.adguard.com (DoQ) - 不支持代理
	// - tcp://8.8.8.8:53 (TCP)
	// - udp://8.8.8.8:53 (UDP) - 仅 direct 出站支持
	// - 8.8.8.8:53 (默认 UDP, 仅 direct 出站支持)
	// - 8.8.8.8 (默认 UDP, 端口 53, 仅 direct 出站支持)
	//
	// 注意: SOCKS5 代理仅支持 HTTPS (DoH) 和 TCP 协议

	// 如果包含 ://，提取协议
	if strings.Contains(nameserver, "://") {
		parts := strings.SplitN(nameserver, "://", 2)
		protocol = parts[0]
		address = parts[1]

		// 对于 HTTPS，保留完整 URL
		if protocol == "https" {
			address = nameserver
			return protocol, address
		}

		// 对于 TLS/QUIC，暂不支持代理（需要额外实现）
		if protocol == "tls" || protocol == "quic" {
			// 返回原始地址，让 AdGuard upstream 处理
			return protocol, nameserver
		}

		// 对于普通 DNS，确保有端口
		if (protocol == "tcp" || protocol == "udp") && !strings.Contains(address, ":") {
			address = address + ":53"
		}

		return protocol, address
	}

	// 没有协议前缀，默认为 UDP (仅 direct 出站支持)
	address = nameserver
	if !strings.Contains(address, ":") {
		// 检查是否是 IP 地址
		if net.ParseIP(address) != nil {
			address = address + ":53"
		}
	}

	return "udp", address // 默认 UDP
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

	// 创建可取消的 context，用于取消其他查询
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	resChan := make(chan result, len(g.upstreams))

	for i, u := range g.upstreams {
		go func(ups upstream.Upstream, nameserver string) {
			queryStart := time.Now()

			// 使用 upstream.Exchange 进行查询
			resp, err := ups.Exchange(m)
			queryLatency := time.Since(queryStart)

			// 检查是否已被取消（第一个成功响应已返回）
			select {
			case <-queryCtx.Done():
				// 查询被取消，不发送结果，不输出日志
				return
			default:
				// 继续发送结果
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
				// 取消其他正在进行的查询
				cancel()

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
			cancel()
			g.logger.Debug("上游查询超时: group=%s domain=%s timeout=%v", g.name, domain, g.timeout)
			return nil, fmt.Errorf("查询超时")
		}
	}

	cancel()
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
