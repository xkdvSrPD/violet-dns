# Violet DNS Server

一个高性能、智能分流的 DNS 服务器实现，支持多上游 DNS 提供商、智能域名分类、ECS (EDNS Client Subnet)、SOCKS5 代理路由和 Redis/内存缓存。

---

## 系统架构

### 核心组件

系统采用分层架构设计，从上到下依次为:

1. **DNS Server Layer**: 负责接收和解析 DNS 查询请求，支持 UDP、TCP 和 DoH 协议
2. **Query Router Layer**: 根据域名分类和策略规则进行智能路由
3. **Middleware Pipeline**: 提供查询去重、缓存查询、响应验证等中间件功能
4. **Upstream Manager Layer**: 管理上游 DNS 组，执行并发查询和 IP 验证
5. **Transport Layer**: 支持多种 DNS 传输协议 (DoH、DoQ、DoT、UDP)
6. **Outbound Layer**: 处理网络出站连接，支持 SOCKS5 代理和直连

### 数据流

```
客户端查询 → DNS Server → Query Router → Middleware Pipeline
→ Upstream Manager → Transport Layer → Outbound Layer → 上游 DNS
```

---

## 核心功能

### 1. DNS Server (端口监听)

DNS 服务器监听指定端口，接收 UDP 查询请求并返回响应。

**实现要点**:
- 使用 `github.com/miekg/dns` 库创建 UDP 服务器
- 监听配置的端口 (默认 10053)
- 处理并发连接，每个查询使用独立 goroutine
- 实现优雅关闭，确保正在处理的查询完成后再退出
- 添加请求超时控制，防止资源耗尽

**注意事项**:
- 启动前需验证端口是否被占用
- 实现 context 传递，支持链路追踪
- 记录每个查询的处理时间和状态
- 异常处理需要完善，避免 panic 导致服务崩溃

### 2. Bootstrap DNS (初始化域名解析)

用于解析配置文件中的所有域名 (如上游 DNS 服务器的域名)。

**实现要点**:
- 使用简单可靠的 DNS 服务器 (如 223.5.5.5、119.29.29.29)
- 在加载配置阶段执行，解析所有 nameserver 中的域名
- 将域名解析为 IP 地址后缓存，避免循环依赖
- 支持 IPv4 和 IPv6 双栈解析

**注意事项**:
- Bootstrap DNS 不能依赖主 DNS 系统本身
- 解析失败应记录警告但不阻止启动
- 提供超时机制，避免长时间等待
- 缓存解析结果，减少重复查询

### 3. Upstream Group (上游 DNS 组)

将 DNS 服务器分为三个组: `proxy`、`proxy_ecs`、`direct`。

**实现要点**:
- **并发查询机制**: 每组内所有服务器同时查询，选择最快响应
- **协议支持**: 支持 DoH (HTTPS) 和 UDP 两种查询方式
- **system 关键字**: 直接使用系统的 DNS 服务器 (读取 /etc/resolv.conf)
- **ECS 注入**:
  - `proxy_ecs` 组强制添加 ECS 信息
  - `proxy` 组根据配置决定是否添加 ECS
  - `direct` 组支持 ECS 但可配置关闭
- **连接池管理**: 对 DoH 使用 HTTP/2 连接池，提高性能
- **超时控制**: 每组配置独立的超时时间

**注意事项**:
- 使用 context.WithTimeout 控制查询超时
- 实现 singleflight 模式，避免重复查询
- 查询失败时根据 `fallback_on_error` 配置决定是否重试
- 记录每个上游服务器的响应时间和成功率
- DoH 查询需要正确处理 HTTP 状态码和 DNS 响应

### 4. Outbound (出站代理)

每个 upstream_group 可以指定一个 outbound (SOCKS5 代理)。

**实现要点**:
- **默认 direct**: 不使用代理直接连接
- **SOCKS5 支持**: 支持用户名密码认证
- **UDP over SOCKS5**: 支持 DNS UDP 查询通过 SOCKS5 代理
- **连接复用**: 复用 SOCKS5 连接，减少握手开销

**注意事项**:
- SOCKS5 连接失败时需要有降级策略
- 支持代理连接超时和重试
- 正确实现 SOCKS5 协议的认证和命令阶段
- DoH 通过 SOCKS5 时需要正确配置 HTTP Client 的 Transport

### 5. ECS (EDNS Client Subnet)

为 DNS 查询添加客户端子网信息，优化 CDN 解析。

**实现要点**:
- **全局配置**: 默认的 IPv4 和 IPv6 ECS 地址
- **组级配置**: 每个 upstream_group 可以覆盖全局 ECS
- **策略级配置**: query_policy 中的 ECS 配置优先级最高
- **前缀长度**: 配置发送的 IP 前缀长度 (如 /24, /96)，保护隐私
- **force_ecs**: proxy_ecs 组强制添加 ECS，即使上游不支持

**注意事项**:
- 使用 `github.com/miekg/dns` 的 EDNS0_SUBNET 类型
- 验证 ECS 地址格式的合法性
- 记录 ECS 是否被上游 DNS 使用
- 支持禁用 ECS 的选项

### 6. Cache (缓存系统)

提供两层缓存: DNS 查询缓存和域名分类缓存。

#### 6.1 DNS Cache (dns_cache)

**实现要点**:
- **严格 TTL**: 缓存过期时间严格遵守上游 DNS 返回的 TTL
- **最大 TTL**: 限制缓存的最长时间 (如 24 小时)
- **Stale Serving**: 上游查询失败时返回过期缓存，提高可用性
- **Negative Cache**: 缓存 NXDOMAIN 等否定响应
- **ARC 算法**: 使用 Adaptive Replacement Cache，比 LRU 更适合 DNS 场景
- **Redis/Memory**: 支持 Redis 和内存两种缓存后端

**注意事项**:
- 缓存键需要包含域名和查询类型 (A、AAAA 等)
- 实现缓存预热和主动刷新机制
- Redis 连接失败时自动降级到内存缓存
- 记录缓存命中率用于性能监控
- `clear: true` 仅用于开发环境，生产环境应禁用

#### 6.2 Category Cache (category_cache)

**实现要点**:
- **域名分类存储**: 存储域名所属的分组 (proxy_site、direct_site 等)
- **长 TTL**: 分类信息变化较少，可以设置较长缓存时间 (如 7 天)
- **动态更新**: 通过 `proxy_ecs_fallback` 机制自动更新未知域名的分类
- **预加载**: 启动时从 dlc.dat 文件加载预定义分类

**注意事项**:
- 分类缓存与 DNS 缓存分开存储
- 支持模糊匹配 (如 *.google.com)
- 记录分类更新的时间和来源
- 提供 API 手动修改域名分类

### 7. Fallback Mechanism (回退机制)

系统提供两种回退机制: Query Policy 回退和 Proxy ECS 回退。

#### 7.1 Query Policy Fallback

**实现要点**:
- **expected_ips 验证**: 查询后检查返回的 IP 是否符合预期规则
- **fallback_group 指定**: IP 不符合时使用指定的组重新查询
- **最终结果**: fallback_group 的查询结果为最终结果，不再验证
- **级联匹配**: 如果没有 fallback_group，继续匹配下一个 query_policy

**查询流程**:
1. 根据域名匹配 query_policy (自上而下)
2. 使用匹配到的 upstream_group 进行查询
3. 如果配置了 expected_ips，验证返回的 IP
4. IP 不符合且配置了 fallback_group → 使用 fallback_group 查询 (最终)
5. IP 不符合且没有 fallback_group → 继续匹配下一个 policy
6. 所有 policy 都不匹配 → 使用 unknown 的策略

**注意事项**:
- expected_ips 支持 geoip 和 ASN 规则 (如 geoip:cn, geoip:!cn, asn:4134)
- 使用 geoip.dat 和 ASN 数据库进行 IP 地理位置匹配
- fallback_group 必须存在于 upstream_group 中
- 记录每次 fallback 的原因和结果
- 避免循环 fallback (A → B → A)

#### 7.2 Proxy ECS Fallback

用于 unknown 域名的智能分流，通过并发查询判断域名属性。

**实现要点**:
- **并发查询**: 同时查询 `proxy_ecs` 和 `proxy` 两个组
- **等待策略**: 等待 proxy_ecs 返回 (最长 3 秒)
- **IP 规则匹配**:
  - 如果 proxy_ecs 返回的 IP 匹配 rule (如 geoip:cn) → fallback 到 `direct` 组
  - 如果不匹配 → 使用 `proxy` 组的结果
- **自动分类**: 根据最终使用的组更新 category_cache
  - 使用 direct → 标记为 `direct_site`
  - 使用 proxy → 标记为 `proxy_site`

**注意事项**:
- 使用 channel 和 select 实现并发查询和超时控制
- proxy_ecs 失败时直接使用 proxy 结果
- 记录 IP 匹配规则的详细信息
- 自动分类需要更新 category_cache 和 Redis
- 避免频繁更新缓存导致性能问题

### 8. Category Policy (域名分类策略)

从外部文件预加载域名分类信息。

**实现要点**:
- **DLC 文件解析**: 解析 V2Ray domain-list-community 的 dlc.dat 文件
- **分组定义**: 在 config.yaml 中定义需要的分组和对应的 dlc 分类
- **预加载**: 启动时将分组数据加载到 category_cache
- **定时更新**: 使用 cron 表达式定时下载和更新分类文件
- **增量更新**: 更新时保留动态学习的分类，只更新预定义分类

**注意事项**:
- dlc.dat 文件格式使用 protobuf，需要正确解析
- 文件下载失败不应阻止启动，使用本地缓存
- 更新时需要加锁，避免并发读写冲突
- 记录分类数量和更新时间
- 支持本地文件路径和 URL 两种方式

### 9. Query Policy (查询策略)

定义域名分组与上游组的映射关系和查询选项。

**实现要点**:
- **自上而下匹配**: 按配置顺序匹配域名分组
- **首次匹配**: 匹配到第一个分组后立即使用对应的策略
- **选项覆盖**: 策略级配置覆盖全局配置
  - `protocol`: 指定使用的协议 (auto、udp、https)
  - `ecs`: 覆盖 ECS 配置
  - `disable_cache`: 禁用缓存
  - `disable_ipv6`: 禁用 IPv6 查询
  - `rewrite_ttl`: 覆盖响应的 TTL
  - `expected_ips`: IP 验证规则
  - `fallback_group`: IP 不符合时的回退组
- **block 策略**: 内置的阻止策略，返回特定响应
  - `nxdomain`: 返回域名不存在
  - `noerror`: 返回空响应
  - `0.0.0.0`: 返回 0.0.0.0

**注意事项**:
- 策略名称必须与 domain_group 中的分组名称一致
- 所有策略都不匹配时自动使用 unknown 策略
- 实现高效的域名匹配算法 (Trie 树)
- 支持完全匹配、前缀匹配和后缀匹配
- 记录每个策略的匹配次数和查询统计

### 10. Logging (日志系统)

提供详细的操作日志和查询日志。

**日志级别**:

- **debug**: 记录所有详细信息
  - Redis 存储操作 (GET、SET、DEL)
  - DNS 查询请求和响应详情
  - Fallback 决策过程和原因
  - 缓存命中/未命中详情
  - IP 验证和规则匹配过程

- **info**: 记录关键信息
  - 查询的域名和类型
  - 返回的响应类型 (A、AAAA、CNAME)
  - 是否发生 fallback 和原因
  - 查询总耗时 (包含网络和处理时间)
  - 是否使用缓存
  - 使用的 upstream_group

**实现要点**:
- 使用结构化日志 (JSON 格式)
- 每个查询分配唯一的 trace_id
- 支持多种输出 (stdout、文件)
- 日志轮转和大小限制
- 性能敏感路径避免过多日志

**注意事项**:
- 日志中不应包含敏感信息 (如密码)
- 高并发下需要异步日志避免阻塞
- 提供日志采样，避免日志爆炸
- 结合 context 传递 trace_id

---

## 配置文件验证

### 启动时校验

系统启动前必须完成以下配置验证:

#### 1. Port (端口)
- 验证端口号在有效范围内 (1-65535)
- 检查端口是否已被占用
- 非 root 用户不能使用 1-1024 端口

#### 2. Bootstrap
- 至少配置一个 nameserver
- nameserver 地址格式合法 (IP 或域名)
- 超时时间合理 (建议 1-10 秒)

#### 3. Upstream Group
- 必须配置 proxy、proxy_ecs、direct 三个组
- 每个组至少有 1 个 nameserver
- nameserver 格式正确 (支持 IP、域名、DoH URL、QUIC URL)
- 指定的 outbound 必须存在 (direct 默认存在)
- strategy 值合法 (ipv4_only、ipv6_only、prefer_ipv4、prefer_ipv6)
- 超时时间合理 (建议 1-30 秒)

#### 4. Outbound
- 暂时只支持 type 为 socks5 和 direct
- SOCKS5 配置包含 server 和 port
- 端口号在有效范围内
- 默认添加 direct 出站 (即使未配置)

#### 5. ECS
- 必须配置 default_ipv4 和 default_ipv6
- IP 地址格式符合 CIDR 规范
- 前缀长度合理 (IPv4: 8-32, IPv6: 32-128)

#### 6. Cache
- type 为 redis 或 memory
- algorithm 为 arc 或 lru
- TTL 值合理 (min_ttl < max_ttl)
- Redis 类型时需要配置 redis 连接信息

#### 7. Redis
- 配置了 server 和 port
- database 为非负整数
- pool_size 为正整数
- 超时时间合理

#### 8. Category Policy
- preload 启用时必须配置 file (URL 或本地路径)
- update 如果配置，必须是合法的 cron 表达式
- domain_group 中的分类必须存在于 dlc 文件中

#### 9. Query Policy
- 策略名称必须与 domain_group 中的分组名称一致
- group 必须存在于 upstream_group 中，或为 block
- block_type 为合法值 (nxdomain、noerror、0.0.0.0)
- 自动添加 unknown 策略 (如果未配置)
- expected_ips 规则格式正确
- fallback_group 如果配置，必须存在于 upstream_group 中

#### 10. Fallback
- geoip 和 asn 文件路径或 URL 必须配置
- update 如果配置，必须是合法的 cron 表达式
- rule 必须配置且至少有一条规则
- rule 格式正确 (geoip:xx, asn:xxxx)

#### 11. Performance
- 数值类型的配置必须为正整数
- query_timeout 应大于 upstream_group 的 timeout
- max_concurrent_queries 合理 (建议 100-10000)

#### 12. Log
- level 为合法值 (debug、info、warn、error)
- format 为 json 或 text
- output 路径可写 (如果输出到文件)

### 运行时检查

启动后进行以下检查:

- **连接测试**: 测试所有 nameserver 是否可达
- **文件下载**: 验证 geoip、dlc、asn 文件是否可下载
- **Redis 连接**: 如果使用 Redis，测试连接是否成功
- **代理测试**: 测试 SOCKS5 代理是否可用

---

## 启动流程

### 第一阶段: 配置加载与验证

1. **读取配置文件**: 解析 YAML 格式的 config.yaml
2. **配置验证**: 执行上述所有配置验证规则
3. **配置加载**: 将配置加载到内存，准备运行时使用
4. **配置可变**: 配置保存在内存中，为后续 API 支持做准备

**注意事项**:
- 验证失败应打印详细错误信息并退出
- 警告信息应记录但不阻止启动
- 使用 `gopkg.in/yaml.v3` 解析 YAML
- 提供配置结构体的详细注释

### 第二阶段: 外部文件下载

1. **检查本地文件**: 检查程序运行目录是否已有文件
2. **下载 dlc.dat**: V2Ray domain-list-community 文件
3. **下载 geoip.dat**: Loyalsoldier/geoip GeoIP 数据库
4. **下载 GeoLite2-ASN.mmdb**: MaxMind ASN 数据库
5. **验证文件**: 检查文件大小和格式是否正确

**注意事项**:
- 文件已存在则跳过下载，减少启动时间
- 下载失败不阻止启动，使用旧文件或禁用相关功能
- 使用 HTTP 客户端的超时和重试机制
- 记录文件下载的进度和结果
- 支持通过代理下载文件

### 第三阶段: 数据预加载

1. **连接 Redis**: 如果配置了 Redis，建立连接池
2. **清空缓存**: 如果 clear: true，清空 Redis 中的数据
3. **解析 DLC 文件**: 读取 dlc.dat 文件
4. **加载域名分组**: 根据 domain_group 配置加载对应分类
5. **写入 Category Cache**: 将分组数据批量写入缓存

**注意事项**:
- 使用批量操作提高 Redis 写入性能 (Pipeline)
- 预加载失败应记录错误但不阻止启动
- 显示预加载进度和统计信息
- 处理大文件时注意内存使用

### 第四阶段: 组件初始化

1. **初始化 GeoIP Matcher**: 加载 geoip.dat 和 ASN 数据库
2. **初始化 Bootstrap DNS**: 创建 bootstrap DNS 客户端
3. **解析域名**: 使用 bootstrap DNS 解析所有 nameserver 域名
4. **初始化 Upstream Manager**: 创建上游 DNS 组管理器
5. **初始化 Cache**: 创建缓存管理器 (DNS cache 和 Category cache)
6. **初始化 Query Router**: 创建查询路由器，加载所有策略

**注意事项**:
- 组件初始化顺序很重要，避免循环依赖
- 每个组件初始化失败应有明确的错误信息
- 提供组件健康检查接口
- 使用依赖注入模式，提高可测试性

### 第五阶段: 启动服务

1. **启动定时任务**: 启动 cron 任务更新分类和 GeoIP 文件
2. **启动 DNS Server**: 在配置的端口监听 UDP 请求
3. **等待信号**: 监听系统信号 (SIGINT, SIGTERM)
4. **优雅关闭**: 收到信号后停止接受新连接，等待现有查询完成

**注意事项**:
- 使用 context 控制整个应用生命周期
- 提供启动成功的明确日志
- 实现 health check 端点 (可选)
- 记录启动总耗时

---

## 实现建议

### 代码结构

建议采用以下目录结构:

```
violet-dns/
├── main.go                 # 程序入口
├── config/                 # 配置相关
│   ├── config.go          # 配置结构体定义
│   ├── validate.go        # 配置验证
│   └── loader.go          # 配置加载
├── server/                 # DNS 服务器
│   ├── server.go          # UDP 服务器
│   └── handler.go         # 查询处理
├── router/                 # 查询路由
│   ├── router.go          # 路由器实现
│   ├── matcher.go         # 域名匹配
│   └── policy.go          # 策略管理
├── upstream/               # 上游管理
│   ├── manager.go         # 上游管理器
│   ├── group.go           # 上游组
│   ├── doh.go             # DoH 实现
│   └── udp.go             # UDP 实现
├── cache/                  # 缓存
│   ├── dns_cache.go       # DNS 缓存
│   ├── category_cache.go  # 分类缓存
│   ├── redis.go           # Redis 后端
│   └── memory.go          # 内存后端
├── outbound/               # 出站管理
│   ├── socks5.go          # SOCKS5 代理
│   └── direct.go          # 直连
├── category/               # 域名分类
│   ├── loader.go          # DLC 文件加载
│   ├── updater.go         # 定时更新
│   └── parser.go          # 文件解析
├── geoip/                  # GeoIP
│   ├── matcher.go         # IP 匹配
│   └── loader.go          # 数据加载
├── middleware/             # 中间件
│   ├── singleflight.go    # 查询去重
│   └── logger.go          # 日志中间件
└── utils/                  # 工具函数
    ├── dns.go             # DNS 工具
    └── download.go        # 文件下载
```

### 设计原则

1. **高可扩展性**
   - 使用接口而非具体实现
   - 每个组件职责单一
   - 支持插件式扩展

2. **高可读性**
   - 清晰的命名规范
   - 详细的注释文档
   - 示例代码和测试

3. **高性能**
   - 并发查询设计
   - 连接池和缓存
   - 避免不必要的内存分配

4. **高可靠性**
   - 完善的错误处理
   - 优雅降级机制
   - 详细的日志记录

### 关键技术选型

- **DNS 库**: `github.com/miekg/dns` - 成熟的 Go DNS 库
- **HTTP 客户端**: 标准库 `net/http` - 支持 HTTP/2 和连接池
- **Redis 客户端**: `github.com/redis/go-redis/v9` - 支持 context 和 pipeline
- **Cron**: `github.com/robfig/cron/v3` - 定时任务调度
- **日志**: `github.com/sirupsen/logrus` 或 `go.uber.org/zap` - 结构化日志
- **缓存**: `github.com/elastic/go-freelru` - ARC 缓存算法实现
- **Singleflight**: `golang.org/x/sync/singleflight` - 查询去重
- **GeoIP**: `github.com/oschwald/maxminddb-golang` - MaxMind 数据库读取

### 性能优化

1. **并发控制**: 使用 goroutine pool 限制并发数
2. **连接复用**: HTTP/2 和 TCP 连接池
3. **内存优化**: 对象池复用，减少 GC 压力
4. **缓存预热**: 启动时预加载热点域名
5. **批量操作**: Redis pipeline 减少网络往返

### 测试策略

1. **单元测试**: 每个组件独立测试，覆盖率 > 80%
2. **集成测试**: 测试组件间协作
3. **性能测试**: 压测并发查询能力
4. **场景测试**: 模拟真实使用场景

---

## 参考项目

以下开源项目提供了宝贵的实现参考:

- **sing-box**: 传输层抽象、RDRC 缓存
- **Xray-core**: IP 过滤、Stale Serving、缓存迁移
- **mihomo**: ARC 缓存、Singleflight、HTTP/3、并发回退
- **geoview**: 域名匹配优化 (Trie vs Regex)

---

## 后续计划

1. **API 接口**: 实现 HTTP API 用于运行时配置修改
2. **Web UI**: 提供 Web 管理界面
3. **监控指标**: 集成 Prometheus metrics
4. **DoT/DoQ**: 支持 DNS over TLS 和 DNS over QUIC
5. **规则订阅**: 支持在线规则订阅和自动更新
6. **智能学习**: 基于查询历史优化分流规则
