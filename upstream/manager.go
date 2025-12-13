package upstream

import (
	"context"
	"fmt"

	"github.com/miekg/dns"
	"violet-dns/config"
	"violet-dns/middleware"
	"violet-dns/outbound"
)

// Manager 上游管理器
type Manager struct {
	groups map[string]*Group
	logger *middleware.Logger
}

// NewManager 创建上游管理器
func NewManager(logger *middleware.Logger) *Manager {
	return &Manager{
		groups: make(map[string]*Group),
		logger: logger,
	}
}

// AddGroup 添加上游组
func (m *Manager) AddGroup(name string, group *Group) {
	m.groups[name] = group
}

// GetGroup 获取上游组
func (m *Manager) GetGroup(name string) (*Group, bool) {
	group, exists := m.groups[name]
	return group, exists
}

// Query 查询指定组
func (m *Manager) Query(ctx context.Context, groupName, domain string, qtype uint16) (*dns.Msg, error) {
	group, exists := m.GetGroup(groupName)
	if !exists {
		return nil, fmt.Errorf("上游组不存在: %s", groupName)
	}

	// DEBUG: 记录开始上游查询
	m.logger.LogUpstreamQuery(domain, qtype, groupName, group.nameservers)

	return group.Query(ctx, domain, qtype)
}

// LoadFromConfig 从配置加载上游组
func (m *Manager) LoadFromConfig(cfg *config.Config, outbounds map[string]outbound.Outbound) error {
	for name, groupCfg := range cfg.UpstreamGroup {
		// 获取 outbound
		ob := outbounds["direct"] // 默认使用 direct
		if groupCfg.Outbound != "" {
			if o, exists := outbounds[groupCfg.Outbound]; exists {
				ob = o
			}
		}

		// 创建组
		group := NewGroup(
			name,
			groupCfg.Nameservers,
			ob,
			groupCfg.Strategy,
			groupCfg.Timeout,
			m.logger,
		)

		// 设置 ECS
		group.SetECS(groupCfg.EnableECS, groupCfg.ForceECS, groupCfg.ECSIP)

		m.AddGroup(name, group)
	}

	return nil
}
