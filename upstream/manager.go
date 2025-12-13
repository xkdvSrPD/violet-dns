package upstream

import (
	"context"
	"fmt"
	"time"

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
	m.logger.LogUpstreamQuery(ctx, domain, qtype, groupName, group.nameservers)

	return group.Query(ctx, domain, qtype)
}

// LoadFromConfig 从配置加载上游组
func (m *Manager) LoadFromConfig(cfg *config.Config, outbounds map[string]outbound.Outbound) error {
	const defaultTimeout = 5 * time.Second // 固定超时时间为 5 秒

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
			defaultTimeout,
			m.logger,
		)

		// 设置 ECS
		// 逻辑:
		// 1. 如果 group 配置了 ecs_ip，使用 group 的配置
		// 2. 如果 group 是 proxy_ecs 且未配置 ecs_ip，且全局 ECS 启用，使用全局默认值
		// 3. 否则不添加 ECS
		ecsIP := groupCfg.ECSIP
		if ecsIP == "" && name == "proxy_ecs" && cfg.ECS.Enable {
			ecsIP = cfg.ECS.DefaultIPv4
		}
		group.SetECS(ecsIP)

		m.AddGroup(name, group)
	}

	return nil
}
