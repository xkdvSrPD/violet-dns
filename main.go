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

	// 先创建一个临时 logger 用于启动阶段（配置还没加载）
	tmpLogger := middleware.NewLogger("info", "text")

	// 阶段 1: 配置加载与验证
	tmpLogger.Info("=== 阶段 1: 配置加载与验证 ===")
	tmpLogger.Info("加载配置文件: %s", *configFile)
	cfg, err := config.LoadAndValidate(*configFile)
	if err != nil {
		tmpLogger.Error("配置加载失败: %v", err)
		os.Exit(1)
	}
	tmpLogger.Info("配置加载成功")

	// 阶段 2: 外部文件下载
	tmpLogger.Info("=== 阶段 2: 外部文件下载 ===")
	if cfg.CategoryPolicy.Preload.Enable {
		if err := utils.DownloadFile(cfg.CategoryPolicy.Preload.File, "dlc.dat"); err != nil {
			tmpLogger.Warn("下载 dlc.dat 失败: %v", err)
		} else {
			tmpLogger.Info("dlc.dat 准备就绪")
		}
	}

	if err := utils.DownloadFile(cfg.Fallback.GeoIP, "Country.mmdb"); err != nil {
		tmpLogger.Warn("下载 Country.mmdb 失败: %v", err)
	} else {
		tmpLogger.Info("Country.mmdb 准备就绪")
	}

	if err := utils.DownloadFile(cfg.Fallback.ASN, "GeoLite2-ASN.mmdb"); err != nil {
		tmpLogger.Warn("下载 GeoLite2-ASN.mmdb 失败: %v", err)
	} else {
		tmpLogger.Info("GeoLite2-ASN.mmdb 准备就绪")
	}

	// 阶段 3: 数据预加载
	tmpLogger.Info("=== 阶段 3: 数据预加载 ===")

	// 创建 Redis 客户端
	var redisClient *redis.Client
	if cfg.Cache.DNSCache.Type == "redis" || cfg.Cache.CategoryCache.Type == "redis" {
		redisClient = redis.NewClient(&redis.Options{
			Addr:         fmt.Sprintf("%s:%d", cfg.Redis.Server, cfg.Redis.Port),
			Password:     cfg.Redis.Password,
			DB:           cfg.Redis.Database,
			MaxRetries:   cfg.Redis.MaxRetries,
			PoolSize:     cfg.Redis.PoolSize,
			DialTimeout:  cfg.Redis.Timeout,
			ReadTimeout:  cfg.Redis.Timeout * 2, // 读取超时设为配置的 2 倍
			WriteTimeout: cfg.Redis.Timeout * 2, // 写入超时设为配置的 2 倍
			PoolTimeout:  cfg.Redis.Timeout * 3, // 连接池超时设为配置的 3 倍
		})

		// 测试连接
		ctx := context.Background()
		if err := redisClient.Ping(ctx).Err(); err != nil {
			tmpLogger.Warn("Redis 连接测试失败: %v", err)
			tmpLogger.Info("将使用内存缓存作为后备方案")
			redisClient = nil
		} else {
			tmpLogger.Info("Redis 连接已建立并验证成功")
		}
	}

	// 创建分类缓存
	var categoryCache cache.CategoryCache
	if cfg.Cache.CategoryCache.Type == "redis" && redisClient != nil {
		categoryCache = cache.NewRedisCategoryCache(redisClient)
		if cfg.Cache.CategoryCache.Clear {
			categoryCache.Clear()
			tmpLogger.Info("已清空分类缓存")
		}
	} else {
		categoryCache = cache.NewMemoryCategoryCache()
	}

	// 预加载域名分类
	if cfg.CategoryPolicy.Preload.Enable {
		loader := category.NewLoader(categoryCache)
		if err := loader.Load("dlc.dat", cfg.CategoryPolicy.Preload.DomainGroup); err != nil {
			tmpLogger.Warn("预加载域名分类失败: %v", err)
		} else {
			tmpLogger.Info("域名分类预加载成功")
		}
	}

	// 阶段 4: 组件初始化
	tmpLogger.Info("=== 阶段 4: 组件初始化 ===")

	// 1. 初始化 Logger（需要最先初始化，其他组件会用到）
	logger := middleware.NewLogger(cfg.Log.Level, cfg.Log.Format)
	logger.Info("Logger 初始化成功")

	// 2. 初始化 GeoIP Matcher
	geoipMatcher, err := geoip.NewMatcher("Country.mmdb", "GeoLite2-ASN.mmdb")
	if err != nil {
		logger.Warn("初始化 GeoIP Matcher 失败: %v", err)
		// 创建一个空的 Matcher 避免 nil 指针
		geoipMatcher, _ = geoip.NewMatcher("", "")
	} else {
		logger.Info("GeoIP Matcher 初始化成功")
	}

	// 3. 初始化 Outbound
	outbounds := make(map[string]outbound.Outbound)
	outbounds["direct"] = outbound.NewDirectOutbound()
	for _, obCfg := range cfg.Outbound {
		if obCfg.Type == "socks5" && obCfg.Enable {
			ob, err := outbound.NewSOCKS5Outbound(obCfg.Server, obCfg.Port, obCfg.Username, obCfg.Password)
			if err != nil {
				logger.Warn("创建 SOCKS5 出站失败: %v", err)
			} else {
				outbounds[obCfg.Tag] = ob
				logger.Info("SOCKS5 出站 %s 创建成功", obCfg.Tag)
			}
		}
	}

	// 4. 初始化 Upstream Manager
	upstreamMgr := upstream.NewManager(logger)
	if err := upstreamMgr.LoadFromConfig(cfg, outbounds); err != nil {
		logger.Error("初始化 Upstream Manager 失败: %v", err)
		os.Exit(1)
	}
	logger.Info("Upstream Manager 初始化成功")

	// 5. 初始化 DNS Cache V2（RR 级别缓存）
	var dnsCache cache.DNSCacheV2
	maxTTL := time.Duration(cfg.Cache.DNSCache.MaxTTL) * time.Second

	if cfg.Cache.DNSCache.Type == "redis" && redisClient != nil {
		dnsCache = cache.NewRedisDNSCacheV2(redisClient, maxTTL)
		if cfg.Cache.DNSCache.Clear {
			dnsCache.Clear()
			logger.Info("已清空 DNS 缓存")
		}
		logger.Info("DNS Cache V2 (Redis) 初始化成功")
	} else {
		dnsCache = cache.NewMemoryDNSCacheV2(maxTTL)
		logger.Info("DNS Cache V2 (Memory) 初始化成功")
	}

	// 6. 初始化 RouterV2（支持 CNAME 链部分缓存）
	queryRouter := router.NewRouterV2(
		upstreamMgr,
		geoipMatcher,
		dnsCache,
		categoryCache,
		logger,
		cfg.Fallback.Rule, // fallback 规则
	)

	// 加载策略
	for _, policyCfg := range cfg.QueryPolicy {
		policy := router.NewPolicy(policyCfg.Name, policyCfg.Group, policyCfg.Options)
		queryRouter.AddPolicy(policy)
	}
	logger.Info("Query Router 初始化成功")

	// 7. 初始化 Singleflight
	singleflight := middleware.NewSingleflight()

	// 阶段 5: 启动服务
	logger.Info("=== 阶段 5: 启动服务 ===")

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
			logger.Warn("启动定时更新失败: %v", err)
		} else {
			logger.Info("定时更新已启动")
		}
	}

	// 启动 DNS Server
	dnsServer := server.NewServer(cfg.Server.Port, cfg.Server.Bind, queryRouter, logger, singleflight)

	go func() {
		if err := dnsServer.Start(ctx); err != nil {
			logger.Error("DNS 服务器错误: %v", err)
			os.Exit(1)
		}
	}()

	// 等待信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Info("=== Violet DNS Server 已启动 ===")
	logger.Info("监听地址: %s:%d", cfg.Server.Bind, cfg.Server.Port)
	logger.Info("按 Ctrl+C 停止服务器")

	<-sigChan

	logger.Info("正在优雅关闭...")
	cancel()

	// 给一些时间让正在处理的查询完成
	time.Sleep(2 * time.Second)

	logger.Info("服务器已停止")
}
