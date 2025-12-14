package outbound

import (
	"context"
	"fmt"
	"net"

	"golang.org/x/net/proxy"
)

// SOCKS5Outbound SOCKS5 代理出站
type SOCKS5Outbound struct {
	server   string
	port     int
	username string
	password string
	dialer   proxy.Dialer
}

// NewSOCKS5Outbound 创建 SOCKS5 出站
func NewSOCKS5Outbound(server string, port int, username, password string) (*SOCKS5Outbound, error) {
	address := fmt.Sprintf("%s:%d", server, port)

	// 创建 SOCKS5 dialer
	var auth *proxy.Auth
	if username != "" {
		auth = &proxy.Auth{
			User:     username,
			Password: password,
		}
	}

	dialer, err := proxy.SOCKS5("tcp", address, auth, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("创建 SOCKS5 dialer 失败: %w", err)
	}

	return &SOCKS5Outbound{
		server:   server,
		port:     port,
		username: username,
		password: password,
		dialer:   dialer,
	}, nil
}

// Dial 建立 TCP 连接
func (o *SOCKS5Outbound) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	// 使用 SOCKS5 dialer
	return o.dialer.Dial(network, address)
}
