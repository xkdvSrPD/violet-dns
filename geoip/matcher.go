package geoip

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/oschwald/maxminddb-golang"
)

// Matcher GeoIP 匹配器
type Matcher struct {
	geoipReader *maxminddb.Reader
	asnReader   *maxminddb.Reader
	dbType      databaseType
}

// databaseType GeoIP 数据库类型
type databaseType uint8

const (
	typeMaxmind databaseType = iota // GeoLite2-Country
	typeSing                        // sing-geoip
	typeMetaV0                      // Meta-geoip0
)

// geoip2Country GeoIP2 Country 结构
type geoip2Country struct {
	Country struct {
		IsoCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

// GeoLite2ASN GeoLite2 ASN 结构
type GeoLite2ASN struct {
	AutonomousSystemNumber       uint32 `maxminddb:"autonomous_system_number"`
	AutonomousSystemOrganization string `maxminddb:"autonomous_system_organization"`
}

// IPInfoASN IPInfo ASN 结构
type IPInfoASN struct {
	ASN  string `maxminddb:"asn"`
	Name string `maxminddb:"name"`
}

// NewMatcher 创建新的 GeoIP 匹配器
func NewMatcher(geoipFile, asnFile string) (*Matcher, error) {
	m := &Matcher{}

	// 加载 GeoIP 数据库
	if geoipFile != "" {
		reader, err := maxminddb.Open(geoipFile)
		if err != nil {
			return nil, fmt.Errorf("打开 GeoIP 文件失败: %w", err)
		}
		m.geoipReader = reader

		// 检测数据库类型
		switch reader.Metadata.DatabaseType {
		case "sing-geoip":
			m.dbType = typeSing
		case "Meta-geoip0":
			m.dbType = typeMetaV0
		default:
			m.dbType = typeMaxmind
		}
	}

	// 加载 ASN 数据库
	if asnFile != "" {
		reader, err := maxminddb.Open(asnFile)
		if err != nil {
			return nil, fmt.Errorf("打开 ASN 文件失败: %w", err)
		}
		m.asnReader = reader
	}

	return m, nil
}

// Close 关闭数据库
func (m *Matcher) Close() error {
	if m.geoipReader != nil {
		m.geoipReader.Close()
	}
	if m.asnReader != nil {
		m.asnReader.Close()
	}
	return nil
}

// Match 匹配 IP 是否符合规则
func (m *Matcher) Match(ip net.IP, rule string) bool {
	if strings.HasPrefix(rule, "geoip:") {
		country := strings.TrimPrefix(rule, "geoip:")
		return m.matchGeoIP(ip, country)
	} else if strings.HasPrefix(rule, "asn:") {
		asnStr := strings.TrimPrefix(rule, "asn:")
		asn, err := strconv.ParseUint(asnStr, 10, 32)
		if err != nil {
			return false
		}
		return m.matchASN(ip, uint32(asn))
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
	if m.geoipReader == nil {
		return false
	}

	codes := m.lookupCode(ip)
	country = strings.ToLower(country)

	for _, code := range codes {
		if code == country {
			return true
		}
	}

	return false
}

// lookupCode 查询 IP 的国家代码
func (m *Matcher) lookupCode(ip net.IP) []string {
	if m.geoipReader == nil {
		return []string{}
	}

	switch m.dbType {
	case typeMaxmind:
		var country geoip2Country
		_ = m.geoipReader.Lookup(ip, &country)
		if country.Country.IsoCode == "" {
			return []string{}
		}
		return []string{strings.ToLower(country.Country.IsoCode)}

	case typeSing:
		var code string
		_ = m.geoipReader.Lookup(ip, &code)
		if code == "" {
			return []string{}
		}
		return []string{code}

	case typeMetaV0:
		var record any
		_ = m.geoipReader.Lookup(ip, &record)
		switch record := record.(type) {
		case string:
			return []string{record}
		case []any: // lookup returned type of slice is []any
			result := make([]string, 0, len(record))
			for _, item := range record {
				result = append(result, item.(string))
			}
			return result
		}
		return []string{}

	default:
		return []string{}
	}
}

// matchASN 匹配 ASN
func (m *Matcher) matchASN(ip net.IP, asn uint32) bool {
	if m.asnReader == nil {
		return false
	}

	asnNum, _ := m.lookupASN(ip)
	return asnNum == asn
}

// lookupASN 查询 IP 的 ASN 号
func (m *Matcher) lookupASN(ip net.IP) (uint32, string) {
	if m.asnReader == nil {
		return 0, ""
	}

	switch m.asnReader.Metadata.DatabaseType {
	case "GeoLite2-ASN", "DBIP-ASN-Lite (compat=GeoLite2-ASN)":
		var result GeoLite2ASN
		_ = m.asnReader.Lookup(ip, &result)
		return result.AutonomousSystemNumber, result.AutonomousSystemOrganization

	case "ipinfo generic_asn_free.mmdb":
		var result IPInfoASN
		_ = m.asnReader.Lookup(ip, &result)
		if len(result.ASN) > 2 && strings.HasPrefix(result.ASN, "AS") {
			asnNum, _ := strconv.ParseUint(result.ASN[2:], 10, 32)
			return uint32(asnNum), result.Name
		}
		return 0, ""

	default:
		return 0, ""
	}
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

	// IPv6 私有地址
	if ip.To4() == nil {
		privateIPv6Blocks := []string{
			"fc00::/7",  // Unique local address
			"fe80::/10", // Link-local
		}
		for _, cidr := range privateIPv6Blocks {
			_, ipNet, _ := net.ParseCIDR(cidr)
			if ipNet.Contains(ip) {
				return true
			}
		}
	}

	return false
}
