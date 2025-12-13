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
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", address)
	if err != nil {
		return nil, err
	}

	// 将 net.Conn 转换为 net.PacketConn
	// 这是一个简化实现，实际应该使用 net.ListenPacket
	return conn.(net.PacketConn), nil
}
