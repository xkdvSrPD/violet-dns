package outbound

import (
	"context"
	"net"
)

// DirectOutbound 直连出站
type DirectOutbound struct{}

// NewDirectOutbound 创建直连出站
func NewDirectOutbound() *DirectOutbound {
	return &DirectOutbound{}
}

// Dial 建立 TCP 连接
func (o *DirectOutbound) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

// DialUDP 建立 UDP 连接
func (o *DirectOutbound) DialUDP(ctx context.Context, address string) (net.PacketConn, error) {
	// 使用 ListenPacket 而不是 Dial 来创建 UDP 连接
	// ListenPacket 返回 PacketConn 接口，适合 UDP 通信
	lc := &net.ListenConfig{}
	return lc.ListenPacket(ctx, "udp", ":0")
}
