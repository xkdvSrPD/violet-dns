package geoip

import (
	"fmt"
	"net"
	"strings"
)

// Matcher GeoIP 匹配器接口
type Matcher struct {
	// 简化实现：使用 map 存储国家代码对应的 IP 段
	// 实际项目中应使用 geoip.dat 和 ASN 数据库
	geoipData map[string][]string // country -> CIDR list
	asnData   map[int][]string    // ASN -> CIDR list
}

// NewMatcher 创建新的 GeoIP 匹配器
func NewMatcher(geoipFile, asnFile string) (*Matcher, error) {
	// 简化实现：实际应该解析 geoip.dat 和 ASN 数据库文件
	return &Matcher{
		geoipData: make(map[string][]string),
		asnData:   make(map[int][]string),
	}, nil
}

// Match 匹配 IP 是否符合规则
func (m *Matcher) Match(ip net.IP, rule string) bool {
	if strings.HasPrefix(rule, "geoip:") {
		country := strings.TrimPrefix(rule, "geoip:")
		return m.matchGeoIP(ip, country)
	} else if strings.HasPrefix(rule, "asn:") {
		// 解析 ASN 号码
		var asn int
		fmt.Sscanf(strings.TrimPrefix(rule, "asn:"), "%d", &asn)
		return m.matchASN(ip, asn)
	}
	return false
}

// MatchAny 匹配 IP 是否符合任一规则
func (m *Matcher) MatchAny(ip net.IP, rules []string) bool {
	for _, rule := range rules {
		if m.Match(ip, rule) {
			return true
		}
	}
	return false
}

// matchGeoIP 匹配 GeoIP
func (m *Matcher) matchGeoIP(ip net.IP, country string) bool {
	// 处理否定规则
	if strings.HasPrefix(country, "!") {
		country = strings.TrimPrefix(country, "!")
		return !m.matchGeoIPPositive(ip, country)
	}

	// 特殊处理 private
	if country == "private" {
		return m.isPrivateIP(ip)
	}

	return m.matchGeoIPPositive(ip, country)
}

// matchGeoIPPositive 正向匹配 GeoIP
func (m *Matcher) matchGeoIPPositive(ip net.IP, country string) bool {
	// 简化实现：实际应该查询 geoip.dat
	cidrs, exists := m.geoipData[country]
	if !exists {
		return false
	}

	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// matchASN 匹配 ASN
func (m *Matcher) matchASN(ip net.IP, asn int) bool {
	// 简化实现：实际应该查询 ASN 数据库
	cidrs, exists := m.asnData[asn]
	if !exists {
		return false
	}

	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// isPrivateIP 判断是否是私有 IP
func (m *Matcher) isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// IPv4 私有地址
	privateIPBlocks := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16", // Link-local
	}

	for _, cidr := range privateIPBlocks {
		_, ipNet, _ := net.ParseCIDR(cidr)
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// LoadGeoIPData 加载 GeoIP 数据
func (m *Matcher) LoadGeoIPData(country string, cidrs []string) {
	m.geoipData[country] = cidrs
}

// LoadASNData 加载 ASN 数据
func (m *Matcher) LoadASNData(asn int, cidrs []string) {
	m.asnData[asn] = cidrs
}
