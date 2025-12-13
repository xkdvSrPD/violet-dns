package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"violet-dns/cache"
	"violet-dns/category"
	"violet-dns/config"
	"violet-dns/geoip"
	"violet-dns/middleware"
	"violet-dns/outbound"
	"violet-dns/router"
	"violet-dns/server"
	"violet-dns/upstream"
	"violet-dns/utils"

	"github.com/redis/go-redis/v9"
)

func main() {
	// 解析命令行参数
	configFile := flag.String("c", "config.yaml", "配置文件路径")
	flag.Parse()

	// 阶段 1: 配置加载与验证
	fmt.Println("=== 阶段 1: 配置加载与验证 ===")
	fmt.Printf("加载配置文件: %s\n", *configFile)
	cfg, err := config.LoadAndValidate(*configFile)
	if err != nil {
		fmt.Printf("配置加载失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("配置加载成功")

	// 阶段 2: 外部文件下载
	fmt.Println("\n=== 阶段 2: 外部文件下载 ===")
	if cfg.CategoryPolicy.Preload.Enable {
		if err := utils.DownloadFile(cfg.CategoryPolicy.Preload.File, "dlc.dat"); err != nil {
			fmt.Printf("警告: 下载 dlc.dat 失败: %v\n", err)
		} else {
			fmt.Println("dlc.dat 准备就绪")
		}
	}

	if err := utils.DownloadFile(cfg.Fallback.GeoIP, "geoip.dat"); err != nil {
		fmt.Printf("警告: 下载 geoip.dat 失败: %v\n", err)
	} else {
		fmt.Println("geoip.dat 准备就绪")
	}

	if err := utils.DownloadFile(cfg.Fallback.ASN, "GeoLite2-ASN.mmdb"); err != nil {
		fmt.Printf("警告: 下载 GeoLite2-ASN.mmdb 失败: %v\n", err)
	} else {
		fmt.Println("GeoLite2-ASN.mmdb 准备就绪")
	}

	// 阶段 3: 数据预加载
	fmt.Println("\n=== 阶段 3: 数据预加载 ===")

	// 创建 Redis 客户端
	var redisClient *redis.Client
	if cfg.Cache.DNSCache.Type == "redis" || cfg.Cache.CategoryCache.Type == "redis" {
		redisClient = redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Server, cfg.Redis.Port),
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.Database,
		})
		fmt.Println("Redis 连接已建立")
	}

	// 创建分类缓存
	var categoryCache cache.CategoryCache
	if cfg.Cache.CategoryCache.Type == "redis" && redisClient != nil {
		categoryCache = cache.NewRedisCategoryCache(redisClient)
		if cfg.Cache.CategoryCache.Clear {
			categoryCache.Clear()
			fmt.Println("已清空分类缓存")
		}
	} else {
		categoryCache = cache.NewMemoryCategoryCache()
	}

	// 预加载域名分类
	if cfg.CategoryPolicy.Preload.Enable {
		loader := category.NewLoader(categoryCache)
		if err := loader.Load("dlc.dat", cfg.CategoryPolicy.Preload.DomainGroup); err != nil {
			fmt.Printf("警告: 预加载域名分类失败: %v\n", err)
		} else {
			fmt.Println("域名分类预加载成功")
		}
	}

	// 阶段 4: 组件初始化
	fmt.Println("\n=== 阶段 4: 组件初始化 ===")

	// 1. 初始化 GeoIP Matcher
	geoipMatcher, err := geoip.NewMatcher("geoip.dat", "GeoLite2-ASN.mmdb")
	if err != nil {
		fmt.Printf("警告: 初始化 GeoIP Matcher 失败: %v\n", err)
	} else {
		fmt.Println("GeoIP Matcher 初始化成功")
	}

	// 2. 初始化 Outbound
	outbounds := make(map[string]outbound.Outbound)
	outbounds["direct"] = outbound.NewDirectOutbound()
	for _, obCfg := range cfg.Outbound {
		if obCfg.Type == "socks5" && obCfg.Enable {
			ob, err := outbound.NewSOCKS5Outbound(obCfg.Server, obCfg.Port, obCfg.Username, obCfg.Password)
			if err != nil {
				fmt.Printf("警告: 创建 SOCKS5 出站失败: %v\n", err)
			} else {
				outbounds[obCfg.Tag] = ob
				fmt.Printf("SOCKS5 出站 %s 创建成功\n", obCfg.Tag)
			}
		}
	}

	// 3. 初始化 Upstream Manager
	upstreamMgr := upstream.NewManager()
	if err := upstreamMgr.LoadFromConfig(cfg, outbounds); err != nil {
		fmt.Printf("错误: 初始化 Upstream Manager 失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Upstream Manager 初始化成功")

	// 4. 初始化 DNS Cache
	var dnsCache cache.DNSCache
	maxTTL := time.Duration(cfg.Cache.DNSCache.MaxTTL) * time.Second
	staleTTL := time.Duration(cfg.Cache.DNSCache.StaleTTL) * time.Second

	if cfg.Cache.DNSCache.Type == "redis" && redisClient != nil {
		dnsCache = cache.NewRedisDNSCache(redisClient, maxTTL, cfg.Cache.DNSCache.ServeStale, staleTTL)
		if cfg.Cache.DNSCache.Clear {
			dnsCache.Clear()
			fmt.Println("已清空 DNS 缓存")
		}
	} else {
		dnsCache = cache.NewMemoryDNSCache(maxTTL, cfg.Cache.DNSCache.ServeStale, staleTTL)
	}
	fmt.Println("DNS Cache 初始化成功")

	// 5. 初始化 Logger
	logger := middleware.NewLogger(cfg.Log.Level, cfg.Log.Format)
	fmt.Println("Logger 初始化成功")

	// 6. 初始化 Router
	queryRouter := router.NewRouter(upstreamMgr, geoipMatcher, dnsCache, categoryCache, logger)

	// 加载域名分组
	parser := category.NewParser()
	dlcData, _ := parser.Parse("dlc.dat")
	domainGroups, _ := parser.ParseDomainGroup(dlcData, cfg.CategoryPolicy.Preload.DomainGroup)
	queryRouter.LoadDomainGroup(domainGroups)

	// 加载策略
	for _, policyCfg := range cfg.QueryPolicy {
		policy := router.NewPolicy(policyCfg.Name, policyCfg.Group, policyCfg.Options)
		queryRouter.AddPolicy(policy)
	}
	fmt.Println("Query Router 初始化成功")

	// 7. 初始化 Singleflight
	singleflight := middleware.NewSingleflight()

	// 阶段 5: 启动服务
	fmt.Println("\n=== 阶段 5: 启动服务 ===")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动定时更新
	if cfg.CategoryPolicy.Preload.Update != "" {
		updater := category.NewUpdater(
			category.NewLoader(categoryCache),
			cfg.CategoryPolicy.Preload.Update,
			"dlc.dat",
			cfg.CategoryPolicy.Preload.DomainGroup,
		)
		if err := updater.Start(ctx); err != nil {
			fmt.Printf("警告: 启动定时更新失败: %v\n", err)
		} else {
			fmt.Println("定时更新已启动")
		}
	}

	// 启动 DNS Server
	dnsServer := server.NewServer(cfg.Server.Port, cfg.Server.Bind, queryRouter, logger, singleflight)

	go func() {
		if err := dnsServer.Start(ctx); err != nil {
			fmt.Printf("DNS 服务器错误: %v\n", err)
			os.Exit(1)
		}
	}()

	// 等待信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("\n=== Violet DNS Server 已启动 ===")
	fmt.Printf("监听地址: %s:%d\n", cfg.Server.Bind, cfg.Server.Port)
	fmt.Println("按 Ctrl+C 停止服务器")

	<-sigChan

	fmt.Println("\n正在优雅关闭...")
	cancel()

	// 给一些时间让正在处理的查询完成
	time.Sleep(2 * time.Second)

	fmt.Println("服务器已停止")
}
