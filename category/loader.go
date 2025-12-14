package category

import (
	"fmt"
)

// CategoryCache 分类缓存接口
type CategoryCache interface {
	Set(domain, category string) error
	Get(domain string) (string, error)
	BatchSet(data map[string]string) error
}

// Loader 域名分类加载器
type Loader struct {
	parser *Parser
	cache  CategoryCache
}

// NewLoader 创建新的加载器
func NewLoader(cache CategoryCache) *Loader {
	return &Loader{
		parser: NewParser(),
		cache:  cache,
	}
}

// Load 加载域名分类数据
func (l *Loader) Load(filename string, domainGroupConfig map[string][]string) error {
	// 解析 DLC 文件 (返回 map[string][]*router.Domain)
	dlcData, err := l.parser.Parse(filename)
	if err != nil {
		return fmt.Errorf("解析 DLC 文件失败: %w", err)
	}

	// 解析域名组配置 (返回 map[string][]string)
	domainGroups, err := l.parser.ParseDomainGroup(dlcData, domainGroupConfig)
	if err != nil {
		return fmt.Errorf("解析域名组配置失败: %w", err)
	}

	// 批量写入缓存
	batchData := make(map[string]string)
	for groupName, domains := range domainGroups {
		for _, domain := range domains {
			batchData[domain] = groupName
		}
	}

	if err := l.cache.BatchSet(batchData); err != nil {
		return fmt.Errorf("写入缓存失败: %w", err)
	}

	return nil
}

// LoadReverse 倒序加载域名分类数据（确保正序时上级分流能覆盖下级分类）
func (l *Loader) LoadReverse(filename string, domainGroupConfig map[string][]string) error {
	// 解析 DLC 文件 (返回 map[string][]*router.Domain)
	dlcData, err := l.parser.Parse(filename)
	if err != nil {
		return fmt.Errorf("解析 DLC 文件失败: %w", err)
	}

	// 解析域名组配置 (返回 map[string][]string)
	domainGroups, err := l.parser.ParseDomainGroup(dlcData, domainGroupConfig)
	if err != nil {
		return fmt.Errorf("解析域名组配置失败: %w", err)
	}

	// 获取 domainGroupConfig 的键顺序（用于倒序遍历）
	var groupOrder []string
	for groupName := range domainGroupConfig {
		groupOrder = append(groupOrder, groupName)
	}

	// 倒序遍历并写入缓存（后面的组先写入，前面的组覆盖）
	batchData := make(map[string]string)
	for i := len(groupOrder) - 1; i >= 0; i-- {
		groupName := groupOrder[i]
		domains := domainGroups[groupName]
		for _, domain := range domains {
			batchData[domain] = groupName
		}
	}

	if err := l.cache.BatchSet(batchData); err != nil {
		return fmt.Errorf("写入缓存失败: %w", err)
	}

	return nil
}
