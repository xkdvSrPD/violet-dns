package router

import (
	"context"

	"github.com/miekg/dns"
)

// QueryRouter DNS 查询路由器接口
type QueryRouter interface {
	// Route 路由查询
	Route(ctx context.Context, domain string, qtype uint16) (*dns.Msg, error)

	// AddPolicy 添加策略
	AddPolicy(policy *Policy)

	// LoadDomainGroup 加载域名分组
	LoadDomainGroup(domainGroups map[string][]string)
}
