package category

import (
	"fmt"
)

// Parser DLC 文件解析器
type Parser struct{}

// NewParser 创建新的解析器
func NewParser() *Parser {
	return &Parser{}
}

// Parse 解析 DLC 文件
func (p *Parser) Parse(filename string) (map[string][]string, error) {
	// 简化实现：实际应该解析 protobuf 格式的 dlc.dat 文件
	// 返回格式: category -> domains
	result := make(map[string][]string)

	// 示例数据
	result["google"] = []string{
		"google.com",
		"googleapis.com",
		"googleusercontent.com",
	}
	result["geolocation-!cn"] = []string{
		"facebook.com",
		"twitter.com",
		"youtube.com",
	}
	result["cn"] = []string{
		"baidu.com",
		"qq.com",
		"taobao.com",
	}
	result["geolocation-cn"] = []string{
		"163.com",
		"sina.com.cn",
		"sohu.com",
	}
	result["category-games"] = []string{
		"steam.com",
		"epicgames.com",
	}
	result["category-ads-all"] = []string{
		"doubleclick.net",
		"googlesyndication.com",
	}

	return result, nil
}

// ParseDomainGroup 解析域名组配置
func (p *Parser) ParseDomainGroup(dlcData map[string][]string, groupConfig map[string][]string) (map[string][]string, error) {
	result := make(map[string][]string)

	for groupName, categories := range groupConfig {
		domains := []string{}
		for _, category := range categories {
			if categoryDomains, exists := dlcData[category]; exists {
				domains = append(domains, categoryDomains...)
			} else {
				return nil, fmt.Errorf("分类 %s 不存在于 DLC 数据中", category)
			}
		}
		result[groupName] = domains
	}

	return result, nil
}
