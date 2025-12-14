package outbound

import (
	"context"
	"net"
)

// Outbound 出站接口
type Outbound interface {
	Dial(ctx context.Context, network, address string) (net.Conn, error)
}
