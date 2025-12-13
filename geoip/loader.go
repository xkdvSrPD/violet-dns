package geoip

import (
	"fmt"
)

// Loader GeoIP 数据加载器
type Loader struct {
	matcher *Matcher
}

// NewLoader 创建新的加载器
func NewLoader(matcher *Matcher) *Loader {
	return &Loader{
		matcher: matcher,
	}
}

// Load 加载 GeoIP 和 ASN 数据
func (l *Loader) Load(geoipFile, asnFile string) error {
	// 加载 GeoIP 数据
	if err := l.loadGeoIP(geoipFile); err != nil {
		return fmt.Errorf("加载 GeoIP 数据失败: %w", err)
	}

	// 加载 ASN 数据
	if err := l.loadASN(asnFile); err != nil {
		return fmt.Errorf("加载 ASN 数据失败: %w", err)
	}

	return nil
}

// loadGeoIP 加载 GeoIP 数据
func (l *Loader) loadGeoIP(filename string) error {
	// 简化实现：实际应该解析 geoip.dat 文件
	// 这里只是示例数据
	l.matcher.LoadGeoIPData("cn", []string{
		"1.0.1.0/24",
		"1.0.2.0/23",
	})

	return nil
}

// loadASN 加载 ASN 数据
func (l *Loader) loadASN(filename string) error {
	// 简化实现：实际应该解析 GeoLite2-ASN.mmdb 文件
	// 这里只是示例数据
	l.matcher.LoadASNData(4134, []string{ // 中国电信
		"1.0.1.0/24",
	})
	l.matcher.LoadASNData(4837, []string{ // 中国联通
		"1.0.2.0/24",
	})

	return nil
}
