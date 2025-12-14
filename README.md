# Violet DNS Server

一个高性能、智能分流的 DNS 服务器，支持多上游 DNS 提供商、域名分类、EDNS Client Subnet、SOCKS5 代理路由和缓存。

## 特性

- **智能分流**: 基于域名分类和 IP 地理位置的智能 DNS 路由
- **多协议支持**: UDP DNS、TCP DNS、DNS-over-HTTPS (DoH)
- **代理支持**: SOCKS5 代理（支持 TCP DNS 和 DoH，UDP DNS 实验性支持）
- **高性能缓存**: 支持内存和 Redis 两种缓存后端，RR 级别缓存
- **CNAME 优化**: 部分 CNAME 链缓存，减少查询延迟
- **GeoIP 匹配**: 基于 IP 地理位置和 ASN 的智能回退
- **域名分类**: 支持 V2Ray domain-list-community 格式
- **ECS 支持**: EDNS Client Subnet，优化 CDN 解析
- **详细日志**: 结构化日志，支持 Trace ID 链路追踪

## 系统架构

```
客户端 DNS 查询
    ↓
DNS Server (UDP)
    ↓
Query Router (策略匹配)
    ↓
├─ Block 策略 → 返回阻止响应
├─ Proxy ECS Fallback → 智能分流
└─ 普通查询
    ↓
Upstream Manager (上游管理)
    ↓
└─ Upstream Group (并发查询)
    ↓
    ├─ 直连 → DNS 服务器
    └─ SOCKS5 代理 → DNS 服务器
        ↓
Cache (缓存结果)
    ↓
返回响应
```

## 核心组件

### 1. DNS Server
- **协议**: UDP (TCP 未实现)
- **端口**: 可配置 (默认 10053)
- **并发处理**: 每个查询独立 goroutine
- **链路追踪**: 每个查询分配唯一 Trace ID

### 2. Query Router
- **CNAME 链缓存**: 部分缓存 CNAME 链，减少查询次数
- **域名分组**: 支持逐级向上匹配 (www.google.com → google.com → com)
- **策略路由**: 支持多种策略 (block、direct、proxy、proxy_ecs_fallback)
- **IP 验证**: 查询后验证 IP 是否符合预期规则
- **Fallback 机制**: IP 不符合时自动回退到其他组

### 3. Upstream Manager
- **并发查询**: 同组内所有 nameserver 并发查询，返回最快响应
- **协议支持**:
  - UDP DNS (8.8.8.8:53)
  - TCP DNS (tcp://8.8.8.8:53)
  - DNS-over-HTTPS (https://dns.google/dns-query)
  - DNS-over-TLS (tls://dns.google, 不支持代理)
  - DNS-over-QUIC (quic://dns.adguard.com, 不支持代理)
- **ECS 注入**: 支持 IPv4 和 IPv6 ECS
- **代理路由**: 支持通过 SOCKS5 代理查询

### 4. Cache System
- **DNS 缓存**: RR 级别缓存 (按 qname + qtype)
- **分类缓存**: 域名 → 分组映射
- **后端支持**: 内存缓存、Redis 缓存
- **TTL 控制**: 严格遵守原始 TTL，支持最大 TTL 限制
- **CNAME 优化**: 部分 CNAME 链缓存

### 5. Outbound (代理)
- **Direct**: 直接连接，支持所有协议 (UDP/TCP/HTTPS/DoT/DoQ)
- **SOCKS5**: 仅支持 TCP 和 HTTPS (DoH) 协议
- **重要限制**:
  - SOCKS5 代理**不支持 UDP DNS 查询**
  - 使用 SOCKS5 代理的 upstream_group **必须使用 HTTPS (DoH) 或 TCP (tcp://) 协议**
  - 配置验证会强制检查此限制

### 6. Domain Classification
- **格式**: V2Ray domain-list-community (dlc.dat)
- **预加载**: 启动时加载到缓存
- **定时更新**: 支持 cron 表达式定时更新
- **属性过滤**: 支持 @cn、@!cn 等属性

### 7. GeoIP Matching
- **数据库**: MaxMind GeoLite2-Country、GeoLite2-ASN
- **规则**: geoip:cn、geoip:!cn、geoip:private、asn:4134
- **用途**: IP 验证和智能回退决策

## 配置说明

### 基本配置

```yaml
# DNS 服务器配置
server:
  port: 10053        # 监听端口
  protocol: udp      # 协议 (目前仅支持 udp)
  bind: 0.0.0.0      # 监听地址

# Bootstrap DNS (用于解析 nameserver 域名)
bootstrap:
  nameservers:
    - 223.5.5.5
    - 119.29.29.29
```

### 上游 DNS 组

必须配置三个组：`proxy`、`proxy_ecs`、`direct`

```yaml
upstream_group:
  # 代理组 (无 ECS) - 仅支持 HTTPS 和 TCP 协议
  proxy:
    nameservers:
      - https://1.1.1.1/dns-query
      - https://8.8.8.8/dns-query
    outbound: hk        # 使用的出站代理

  # 代理组 (带 ECS) - 仅支持 HTTPS 协议
  proxy_ecs:
    nameservers:
      - https://dns.google/dns-query
    outbound: hk
    ecs_ip: 1.2.3.4/24  # 组级 ECS IP

  # 直连组 - 支持所有协议
  direct:
    nameservers:
      - 10.115.15.1     # 本地 DNS (UDP)
      - tcp://8.8.8.8:53  # TCP DNS
      - https://dns.google/dns-query  # DoH
    outbound: direct
```

**重要**: 使用 SOCKS5 出站的 upstream_group 只能配置 `https://` 或 `tcp://` 协议的 nameserver。

### 出站代理

```yaml
outbound:
  - tag: direct
    type: direct        # 内置，直接连接

  - tag: hk
    type: socks5
    enable: true
    server: 127.0.0.1
    port: 1080
    username: user      # 可选
    password: pass      # 可选
```

**注意**:
- SOCKS5 代理仅支持 TCP 和 HTTPS (DoH) 协议
- UDP DNS 查询不支持通过 SOCKS5 代理

### ECS 配置

```yaml
ecs:
  enable: true
  default_ipv4: 1.2.3.4/24
  default_ipv6: 2001:db8::/64
  ipv4_prefix: 24       # 注意: 当前固定为 /24
  ipv6_prefix: 64       # 注意: 当前固定为 /56
```

### 缓存配置

```yaml
cache:
  dns_cache:
    enable: true
    clear: false        # 生产环境应为 false
    type: redis         # redis 或 memory

  category_cache:
    enable: true
    clear: false
    type: redis
    ttl: 604800         # 7 天

# Redis 配置
redis:
  server: localhost
  port: 6379
  database: 0
  password: ""
  max_retries: 3
  pool_size: 10
```

### 域名分类

```yaml
category_policy:
  preload:
    enable: true
    file: 'https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat'
    update: '0 0 3 * * *'  # 每天 3 点更新
    domain_group:
      proxy_site:
        - google
        - geolocation-!cn
      direct_site:
        - cn
      ads_site:
        - category-ads-all
```

### 查询策略

```yaml
query_policy:
  # 阻止广告
  - name: ads_site
    group: block
    options:
      block_type: nxdomain  # nxdomain、noerror、0.0.0.0
      block_ttl: 60

  # 代理站点
  - name: proxy_site
    group: proxy
    options:
      expected_ips:         # IP 验证规则
        - geoip:!cn
        - geoip:private
      fallback_group: direct  # IP 不符合时回退到 direct

  # 国内站点
  - name: direct_site
    group: direct
    options:
      expected_ips:
        - geoip:cn

  # 未知域名 (智能回退)
  - name: unknown
    group: proxy_ecs_fallback
    options:
      auto_categorize: true  # 自动更新分类缓存
```

### Fallback 配置

```yaml
fallback:
  geoip: 'https://raw.githubusercontent.com/Loyalsoldier/geoip/release/GeoLite2-Country.mmdb'
  asn: 'https://raw.githubusercontent.com/Loyalsoldier/geoip/release/GeoLite2-ASN.mmdb'
  update: '0 0 7 * * *'
  strategy: race          # 并发查询策略
  rule:                   # 回退到 direct 的规则
    - geoip:cn
    - geoip:private
    - asn:4134            # 中国电信
```

### 性能配置

```yaml
performance:
  max_concurrent_queries: 1000  # 注意: 当前未实现限制
```

### 日志配置

```yaml
log:
  level: debug          # debug, info, warn, error
  format: json          # json 或 text
  output: stdout        # 当前仅支持 stdout
```

## 安装和使用

### 编译

```bash
go build -o violet-dns
```

### 运行

```bash
# 使用默认配置文件 (config.yaml)
./violet-dns

# 指定配置文件
./violet-dns -c /path/to/config.yaml

# 指定运行目录
./violet-dns -d /path/to/runtime/dir
```

### 测试查询

```bash
# 查询域名
dig @127.0.0.1 -p 10053 google.com

# 查询 AAAA 记录
dig @127.0.0.1 -p 10053 AAAA google.com
```

## 工作原理

### 查询流程

1. **接收查询**: DNS Server 接收客户端的 UDP 查询
2. **CNAME 缓存解析**: 尝试从缓存解析 CNAME 链
3. **域名匹配**: 查找域名所属分组 (使用 category_cache)
4. **策略选择**: 根据分组选择对应的 query_policy
5. **特殊处理**:
   - Block 策略: 直接返回阻止响应
   - Proxy ECS Fallback: 并发查询 proxy_ecs 和 proxy，智能选择
6. **上游查询**: 使用对应的 upstream_group 查询
7. **IP 验证**: 检查返回的 IP 是否符合 expected_ips
8. **Fallback**: IP 不符合时:
   - 有 fallback_group: 使用 fallback_group 查询 (最终结果)
   - 无 fallback_group: 继续匹配下一个 policy
9. **CNAME 合并**: 合并缓存的 CNAME 链
10. **缓存结果**: 将查询结果缓存
11. **返回响应**: 返回给客户端

### Proxy ECS Fallback 机制

用于未知域名的智能分流:

1. **并发查询**: 同时查询 proxy_ecs 和 proxy 组
2. **等待 proxy_ecs**: 最长等待 3 秒
3. **IP 规则匹配**:
   - proxy_ecs 返回的 IP 匹配 fallback.rule → 使用 direct 组
   - 不匹配 → 使用 proxy 组结果
4. **自动分类**: 根据最终使用的组更新 category_cache

### 上游并发查询

同一组内的所有 nameserver 并发查询:

1. 启动所有 nameserver 的查询 goroutine
2. 等待第一个成功响应
3. 取消其他正在进行的查询
4. 返回最快的响应

### SOCKS5 代理

- **TCP DNS**: 通过 SOCKS5 TCP CONNECT (tcp://8.8.8.8:53)
- **DoH**: 自定义 HTTP Transport 使用 SOCKS5 (https://dns.google/dns-query)
- **UDP DNS**: 不支持

**配置要求**:
- 使用 SOCKS5 代理的 upstream_group 必须配置 HTTPS 或 TCP nameserver
- 配置验证会自动检查并拒绝不符合要求的配置

## 启动流程

### 阶段 1: 配置加载与验证
- 读取配置文件
- 验证所有配置项
- 检查必需组和引用

### 阶段 2: 外部文件下载
- 下载 dlc.dat (V2Ray domain-list-community)
- 下载 Country.mmdb (GeoIP 数据库)
- 下载 GeoLite2-ASN.mmdb (ASN 数据库)

### 阶段 3: 数据预加载
- 连接 Redis (如果配置)
- 解析 DLC 文件
- 加载域名分组到 category_cache
- 批量写入 Redis

### 阶段 4: 组件初始化
- 初始化 GeoIP Matcher
- 初始化 Bootstrap DNS
- 初始化 Upstream Manager
- 初始化 Cache
- 初始化 Query Router

### 阶段 5: 启动服务
- 启动定时任务 (更新分类和 GeoIP)
- 启动 DNS Server (UDP)
- 等待系统信号
- 优雅关闭

## 日志示例

```json
{
  "level": "info",
  "time": "2025-12-14T10:00:00+08:00",
  "msg": "DNS查询开始",
  "trace_id": "abc123",
  "domain": "google.com",
  "qtype": "A"
}

{
  "level": "debug",
  "time": "2025-12-14T10:00:00+08:00",
  "msg": "域名分类匹配",
  "trace_id": "abc123",
  "domain": "google.com",
  "category": "proxy_site"
}

{
  "level": "info",
  "time": "2025-12-14T10:00:00+08:00",
  "msg": "DNS查询完成",
  "trace_id": "abc123",
  "domain": "google.com",
  "total_latency": "50ms",
  "cache_hit": false,
  "group": "proxy"
}
```

## 性能优化

- **并发查询**: 同组 nameserver 并发，选择最快响应
- **连接复用**: HTTP/2 连接池用于 DoH 查询
- **多级缓存**: RR 级别缓存 + CNAME 链缓存
- **批量操作**: Redis 使用 Pipeline 批量写入
- **查询去重**: Singleflight 避免重复查询

## 已知限制

1. **TCP DNS Server**: 配置项存在但未实现
2. **SOCKS5 UDP**: 不支持，必须使用 HTTPS (DoH) 或 TCP 协议
3. **SOCKS5 代理限制**: 使用 SOCKS5 代理的 upstream_group 只能配置 HTTPS 或 TCP nameserver
4. **并发限制**: max_concurrent_queries 配置当前未实现
5. **日志输出**: 当前仅支持 stdout
6. **DoT/DoQ 代理**: 不支持通过 SOCKS5 代理

## 技术栈

- **DNS 库**: github.com/miekg/dns
- **HTTP 客户端**: net/http (支持 HTTP/2)
- **Redis 客户端**: github.com/redis/go-redis/v9
- **Cron**: github.com/robfig/cron/v3
- **日志**: github.com/sirupsen/logrus
- **GeoIP**: github.com/oschwald/maxminddb-golang
- **SOCKS5**: golang.org/x/net/proxy
- **Singleflight**: golang.org/x/sync/singleflight

## 参考项目

- **mihomo**: 并发回退、ARC 缓存、HTTP/3 支持
- **sing-box**: 传输层抽象、RDRC 缓存
- **Xray-core**: IP 过滤、Stale Serving

## 后续计划

- [ ] TCP DNS Server 支持
- [ ] 并发限制实现
- [ ] Stale Serving (上游失败时返回过期缓存)
- [ ] 日志轮转和文件输出
- [ ] HTTP API 接口
- [ ] Web UI
- [ ] Prometheus Metrics
- [ ] DoT/DoQ 代理支持

## 许可证

MIT License

## 贡献

欢迎提交 Issue 和 Pull Request。
