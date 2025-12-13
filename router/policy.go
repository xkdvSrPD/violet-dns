package router

import (
	"violet-dns/config"
)

// Policy 查询策略
type Policy struct {
	Name    string
	Group   string
	Options config.QueryPolicyOptions
}

// NewPolicy 创建新策略
func NewPolicy(name, group string, options config.QueryPolicyOptions) *Policy {
	return &Policy{
		Name:    name,
		Group:   group,
		Options: options,
	}
}
