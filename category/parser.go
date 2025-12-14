package category

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"google.golang.org/protobuf/proto"
	"violet-dns/component/geodata/router"
)

// Parser DLC 文件解析器
type Parser struct{}

// NewParser 创建新的解析器
func NewParser() *Parser {
	return &Parser{}
}

// Parse 解析 DLC 文件 (protobuf 格式)
func (p *Parser) Parse(filename string) (map[string][]*router.Domain, error) {
	// 读取文件
	data, err := p.readFile(filename)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	// 解析 protobuf
	var geositeList router.GeoSiteList
	if err := proto.Unmarshal(data, &geositeList); err != nil {
		return nil, fmt.Errorf("解析 protobuf 失败: %w", err)
	}

	// 转换为 map: category -> domains
	result := make(map[string][]*router.Domain)
	for _, site := range geositeList.Entry {
		// 将 category 转为小写以支持大小写不敏感匹配
		categoryName := strings.ToLower(site.CountryCode)
		result[categoryName] = site.Domain
	}

	return result, nil
}

// readFile 读取文件内容
func (p *Parser) readFile(filename string) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(file)
}

// ParseDomainGroup 解析域名组配置，支持属性过滤
func (p *Parser) ParseDomainGroup(dlcData map[string][]*router.Domain, groupConfig map[string][]string) (map[string][]string, error) {
	result := make(map[string][]string)

	for groupName, categorySpecs := range groupConfig {
		domains := []string{}
		for _, spec := range categorySpecs {
			// 解析 spec: 支持 "category@attr1@attr2" 或 "category" 格式
			categoryDomains, err := p.parseCategorySpec(dlcData, spec)
			if err != nil {
				return nil, fmt.Errorf("解析分类 %s 失败: %w", spec, err)
			}
			domains = append(domains, categoryDomains...)
		}
		result[groupName] = domains
	}

	return result, nil
}

// parseCategorySpec 解析单个分类规则，支持属性过滤和取反
// 格式:
//   - "google" - 匹配 google 分类的所有域名
//   - "geolocation-!cn" - 匹配 geolocation-!cn 分类
//   - "geolocation-cn@!cn" - 匹配 geolocation-cn 分类中不包含 cn 属性的域名
//   - "geolocation-cn@cn" - 匹配 geolocation-cn 分类中包含 cn 属性的域名
func (p *Parser) parseCategorySpec(dlcData map[string][]*router.Domain, spec string) ([]string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("分类规则不能为空")
	}

	// 分割 category 和 attributes
	// 例如: "geolocation-cn@!cn" -> listName="geolocation-cn", attrFilters=["!cn"]
	parts := strings.Split(spec, "@")
	listName := strings.ToLower(strings.TrimSpace(parts[0]))
	attrFilters := parts[1:]

	// 检查 category 是否存在
	domainList, exists := dlcData[listName]
	if !exists {
		return nil, fmt.Errorf("分类 %s 不存在于 DLC 数据中", spec)
	}

	// 如果没有属性过滤，返回所有域名
	if len(attrFilters) == 0 {
		return p.extractDomainValues(domainList), nil
	}

	// 应用属性过滤
	filtered := p.filterDomainsByAttributes(domainList, attrFilters)
	if len(filtered) == 0 {
		log.Printf("警告: 分类 %s 应用属性过滤后没有匹配的域名\n", spec)
	}

	return p.extractDomainValues(filtered), nil
}

// filterDomainsByAttributes 根据属性过滤域名
// attrFilters 支持:
//   - "cn" - 必须包含 cn 属性
//   - "!cn" - 必须不包含 cn 属性
func (p *Parser) filterDomainsByAttributes(domains []*router.Domain, attrFilters []string) []*router.Domain {
	result := []*router.Domain{}

	for _, domain := range domains {
		if p.matchAllAttributes(domain, attrFilters) {
			result = append(result, domain)
		}
	}

	return result
}

// matchAllAttributes 检查域名是否匹配所有属性过滤器 (AND 逻辑)
func (p *Parser) matchAllAttributes(domain *router.Domain, attrFilters []string) bool {
	for _, filter := range attrFilters {
		filter = strings.ToLower(strings.TrimSpace(filter))
		if filter == "" {
			continue
		}

		// 检查是否是取反属性 (以 ! 开头)
		negate := false
		attrKey := filter
		if filter[0] == '!' {
			negate = true
			attrKey = filter[1:]
		}

		// 检查域名是否有该属性
		hasAttr := p.hasAttribute(domain, attrKey)

		// 应用取反逻辑
		if negate {
			// 如果是取反，域名不应该有这个属性
			if hasAttr {
				return false
			}
		} else {
			// 如果不是取反，域名必须有这个属性
			if !hasAttr {
				return false
			}
		}
	}

	return true
}

// hasAttribute 检查域名是否有指定的属性
func (p *Parser) hasAttribute(domain *router.Domain, attrKey string) bool {
	for _, attr := range domain.Attribute {
		if strings.EqualFold(attr.GetKey(), attrKey) {
			return true
		}
	}
	return false
}

// extractDomainValues 从 Domain 列表中提取域名字符串
func (p *Parser) extractDomainValues(domains []*router.Domain) []string {
	result := make([]string, 0, len(domains))
	for _, domain := range domains {
		result = append(result, domain.Value)
	}
	return result
}
