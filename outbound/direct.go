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
