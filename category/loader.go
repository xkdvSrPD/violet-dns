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
	// 解析 DLC 文件
	dlcData, err := l.parser.Parse(filename)
	if err != nil {
		return fmt.Errorf("解析 DLC 文件失败: %w", err)
	}

	// 解析域名组配置
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
