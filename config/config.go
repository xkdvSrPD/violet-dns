package config

import (
	"time"
)

// Config 主配置结构
type Config struct {
	Server         ServerConfig                    `yaml:"server"`
	Bootstrap      BootstrapConfig                 `yaml:"bootstrap"`
	UpstreamGroup  map[string]*UpstreamGroupConfig `yaml:"upstream_group"`
	Outbound       []OutboundConfig                `yaml:"outbound"`
	ECS            ECSConfig                       `yaml:"ecs"`
	Cache          CacheConfig                     `yaml:"cache"`
	Redis          RedisConfig                     `yaml:"redis"`
	CategoryPolicy CategoryPolicyConfig            `yaml:"category_policy"`
	QueryPolicy    []QueryPolicyConfig             `yaml:"query_policy"`
	Fallback       FallbackConfig                  `yaml:"fallback"`
	Performance    PerformanceConfig               `yaml:"performance"`
	Log            LogConfig                       `yaml:"log"`
	Validation     ValidationConfig                `yaml:"validation"`
}

// ServerConfig DNS 服务器配置
type ServerConfig struct {
	Port     int    `yaml:"port"`
	Protocol string `yaml:"protocol"` // udp, tcp, both
	Bind     string `yaml:"bind"`
}

// BootstrapConfig Bootstrap DNS 配置
type BootstrapConfig struct {
	Nameservers []string      `yaml:"nameservers"`
	Timeout     time.Duration `yaml:"timeout"`
}

// UpstreamGroupConfig 上游 DNS 组配置
type UpstreamGroupConfig struct {
	Nameservers       []string      `yaml:"nameservers"`
	Outbound          string        `yaml:"outbound"`
	Strategy          string        `yaml:"strategy"` // ipv4_only, ipv6_only, prefer_ipv4, prefer_ipv6
	ConcurrentQueries bool          `yaml:"concurrent_queries"`
	EnableECS         bool          `yaml:"enable_ecs"`
	ForceECS          bool          `yaml:"force_ecs"`
	ECSIP             string        `yaml:"ecs_ip"`
	Timeout           time.Duration `yaml:"timeout"`
	FallbackOnError   bool          `yaml:"fallback_on_error"`
}

// OutboundConfig 出站配置
type OutboundConfig struct {
	Tag      string `yaml:"tag"`
	Type     string `yaml:"type"` // direct, socks5
	Enable   bool   `yaml:"enable"`
	Server   string `yaml:"server"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	UDP      bool   `yaml:"udp"`
}

// ECSConfig ECS 配置
type ECSConfig struct {
	Enable      bool   `yaml:"enable"`
	DefaultIPv4 string `yaml:"default_ipv4"`
	DefaultIPv6 string `yaml:"default_ipv6"`
	IPv4Prefix  int    `yaml:"ipv4_prefix"`
	IPv6Prefix  int    `yaml:"ipv6_prefix"`
}

// CacheConfig 缓存配置
type CacheConfig struct {
	DNSCache      DNSCacheConfig      `yaml:"dns_cache"`
	CategoryCache CategoryCacheConfig `yaml:"category_cache"`
}

// DNSCacheConfig DNS 缓存配置
type DNSCacheConfig struct {
	Enable      bool   `yaml:"enable"`
	Clear       bool   `yaml:"clear"`
	Type        string `yaml:"type"`      // redis, memory
	Algorithm   string `yaml:"algorithm"` // arc, lru
	MaxTTL      int    `yaml:"max_ttl"`
	ServeStale  bool   `yaml:"serve_stale"`
	StaleTTL    int    `yaml:"stale_ttl"`
	NegativeTTL int    `yaml:"negative_ttl"`
}

// CategoryCacheConfig 分类缓存配置
type CategoryCacheConfig struct {
	Enable bool   `yaml:"enable"`
	Clear  bool   `yaml:"clear"`
	Type   string `yaml:"type"` // redis, memory
	TTL    int    `yaml:"ttl"`
}

// RedisConfig Redis 配置
type RedisConfig struct {
	Server     string        `yaml:"server"`
	Port       int           `yaml:"port"`
	Database   int           `yaml:"database"`
	Password   string        `yaml:"password"`
	MaxRetries int           `yaml:"max_retries"`
	PoolSize   int           `yaml:"pool_size"`
	Timeout    time.Duration `yaml:"timeout"`
}

// CategoryPolicyConfig 分类策略配置
type CategoryPolicyConfig struct {
	Preload PreloadConfig `yaml:"preload"`
}

// PreloadConfig 预加载配置
type PreloadConfig struct {
	Enable      bool                `yaml:"enable"`
	File        string              `yaml:"file"`
	Update      string              `yaml:"update"` // cron 表达式
	DomainGroup map[string][]string `yaml:"domain_group"`
}

// QueryPolicyConfig 查询策略配置
type QueryPolicyConfig struct {
	Name    string             `yaml:"name"`
	Group   string             `yaml:"group"`
	Options QueryPolicyOptions `yaml:"options"`
}

// QueryPolicyOptions 查询策略选项
type QueryPolicyOptions struct {
	Protocol        string        `yaml:"protocol"` // auto, udp, https
	DisableCache    bool          `yaml:"disable_cache"`
	DisableIPv6     bool          `yaml:"disable_ipv6"`
	RewriteTTL      int           `yaml:"rewrite_ttl"`
	ECS             string        `yaml:"ecs"`
	ExpectedIPs     []string      `yaml:"expected_ips"`
	FallbackGroup   string        `yaml:"fallback_group"`
	BlockType       string        `yaml:"block_type"` // nxdomain, noerror, 0.0.0.0
	FallbackTimeout time.Duration `yaml:"fallback_timeout"`
	AutoCategorize  bool          `yaml:"auto_categorize"`
}

// FallbackConfig 回退配置
type FallbackConfig struct {
	GeoIP    string        `yaml:"geoip"`
	ASN      string        `yaml:"asn"`
	Update   string        `yaml:"update"`   // cron 表达式
	Strategy string        `yaml:"strategy"` // race
	Timeout  time.Duration `yaml:"timeout"`
	Rule     []string      `yaml:"rule"`
}

// PerformanceConfig 性能配置
type PerformanceConfig struct {
	Singleflight         bool          `yaml:"singleflight"`
	QueryTimeout         time.Duration `yaml:"query_timeout"`
	MaxConcurrentQueries int           `yaml:"max_concurrent_queries"`
	Prefetch             bool          `yaml:"prefetch"`
	PrefetchThreshold    float64       `yaml:"prefetch_threshold"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // json, text
	Output string `yaml:"output"` // stdout, file path
}

// ValidationConfig 验证配置
type ValidationConfig struct {
	Strict            bool                     `yaml:"strict"`
	CheckConnectivity bool                     `yaml:"check_connectivity"`
	CheckFiles        bool                     `yaml:"check_files"`
	Warnings          ValidationWarningsConfig `yaml:"warnings"`
}

// ValidationWarningsConfig 验证警告配置
type ValidationWarningsConfig struct {
	LargeDomainGroups int  `yaml:"large_domain_groups"`
	ManyNameservers   int  `yaml:"many_nameservers"`
	ClearCacheEnabled bool `yaml:"clear_cache_enabled"`
	MissingRetry      bool `yaml:"missing_retry"`
}
