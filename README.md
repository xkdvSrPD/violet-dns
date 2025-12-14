# violet-dns

自动 DNS 代理服务器，支持域名分类、GeoIP 路由、ECS、多级缓存和自动回退。

## 特性

- **自动路由** - 基于域名分类的查询策略，支持直连、代理、阻断
- **GeoIP/ASN 匹配** - IP 验证和地理位置判断
- **CNAME 链缓存** - RR 级别缓存，支持 CNAME 链部分命中
- **ECS 支持** - EDNS Client Subnet，可按组配置
- **并发回退** - proxy_ecs_fallback 策略并发查询多个上游，自动选择最优结果
- **多级缓存** - DNS 缓存和域名分类缓存，支持 Redis 和内存两种后端
- **代理支持** - 上游 DNS 和文件下载支持 SOCKS5 代理
- **自动更新** - 定时更新域名分类和 GeoIP 数据库
- **高性能** - Singleflight 去重，连接池复用

## 快速开始

### 安装

```bash
go build
```

### 配置

复制示例配置：

```bash
cp run/config.yaml config.yaml
```

主要配置项：

```yaml
server:
  port: 53              # DNS 端口
  bind: "0.0.0.0"       # 监听地址

upstream_group:
  direct:               # 直连组
    nameservers: ["223.5.5.5", "119.29.29.29"]
  proxy:                # 代理组（无 ECS）
    nameservers: ["https://dns.google/dns-query"]
    outbound: "proxy"
  proxy_ecs:            # 代理组（带 ECS）
    nameservers: ["https://dns.google/dns-query"]
    outbound: "proxy"
    ecs_ip: "8.8.8.8"

outbound:
  - tag: "proxy"
    type: "socks5"
    enable: true
    server: "127.0.0.1"
    port: 1080

query_policy:
  - name: "cn_site"     # 国内域名走直连
    group: "direct"
  - name: "proxy_site"  # 代理域名走代理
    group: "proxy"
    options:
      disable_https: true
  - name: "block_site"  # 阻断域名
    group: "block"
    options:
      block_type: "nxdomain"
```

### 运行

```bash
# 普通模式
./violet-dns

# 指定配置文件
./violet-dns -c /path/to/config.yaml

# 指定运行目录
./violet-dns -d /path/to/runtime

# 预加载模式（加载域名分类到 Redis）
./violet-dns -load
```

## 核心概念

### 查询流程

```
DNS 查询
  ↓
CNAME 链缓存解析（部分命中）
  ↓
域名分类匹配（cn_site/proxy_site/block_site/unknown）
  ↓
查询策略匹配（direct/proxy/proxy_ecs/block/proxy_ecs_fallback）
  ↓
上游组查询（带 ECS、代理）
  ↓
IP 验证（expected_ips）
  ↓
回退组查询（fallback_group，如果验证失败）
  ↓
RR 级别缓存
  ↓
返回结果
```

### 域名分类

域名分类存储在 `dlc.dat`（来自 v2ray/domain-list-community），支持三种匹配方式：

- **完整匹配** - `example.com` 只匹配 `example.com`
- **域名匹配** - `domain:example.com` 匹配 `example.com` 和所有子域名
- **关键字匹配** - `keyword:google` 匹配包含 `google` 的所有域名

分类缓存支持两种模式：

1. **按需查询**（默认）- 查询时从 dlc.dat 读取，结果缓存到 Redis/内存
2. **预加载**（`-load` 模式）- 启动时将所有分类加载到 Redis

### 查询策略

每个域名分类对应一个查询策略，策略指定：

- **上游组** - 使用哪个 upstream_group
- **策略选项** - disable_cache, disable_https, ecs, expected_ips, fallback_group 等

#### 特殊策略

**block** - 阻断域名，返回指定类型的响应：
- `nxdomain` - NXDOMAIN（域名不存在）
- `noerror` - NOERROR（空响应）
- `0.0.0.0` - 返回 0.0.0.0

**proxy_ecs_fallback** - 并发查询策略（未分类域名的默认策略）：
1. 并发查询 `proxy_ecs` 和 `proxy` 组
2. 检查 `proxy_ecs` 结果的 IP 是否匹配 `fallback.rule`（GeoIP 规则）
3. 如果匹配，查询 `direct` 组并返回
4. 否则返回 `proxy` 或 `proxy_ecs` 结果
5. 自动将域名分类为 `direct_site` 或 `proxy_site`

### IP 验证与回退

策略可以配置 `expected_ips`（GeoIP 规则数组），验证上游返回的 IP：

```yaml
query_policy:
  - name: "cn_site"
    group: "proxy"
    options:
      expected_ips: ["geoip:cn"]      # 期望返回国内 IP
      fallback_group: "direct"        # 如果不是国内 IP，使用 direct 组重查
```

验证逻辑：
- 所有返回的 IP 必须匹配 expected_ips 中的任一规则
- 如果验证失败且配置了 `fallback_group`，使用该组重新查询（最终结果，不再验证）
- 如果没有配置 `fallback_group`，回退到 `proxy_ecs_fallback` 策略

### GeoIP 规则

支持的规则格式：

- `geoip:cn` - 国家代码（ISO 3166-1 alpha-2）
- `geoip:private` - 私有 IP（10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16）
- `asn:13335` - ASN 号（自治系统号）

### CNAME 链缓存

DNS 缓存按 RR 记录级别存储（不是完整的 DNS 消息），支持 CNAME 链部分命中：

**示例：** `a.com -> b.com -> c.com -> 1.2.3.4`

1. 第一次查询 `a.com`，缓存所有 RR：
   - `a.com CNAME b.com`
   - `b.com CNAME c.com`
   - `c.com A 1.2.3.4`

2. `c.com A` 记录过期后再次查询 `a.com`：
   - 从缓存返回 `a.com CNAME b.com` 和 `b.com CNAME c.com`
   - 只查询上游 `c.com A`
   - 合并结果返回

### ECS（EDNS Client Subnet）

ECS 在上游组级别配置：

```yaml
upstream_group:
  proxy_ecs:
    nameservers: ["https://dns.google/dns-query"]
    ecs_ip: "8.8.8.8"              # 固定 ECS IP
  proxy_ecs_auto:
    nameservers: ["https://dns.google/dns-query"]
    ecs_ip: ""                     # 使用全局默认 ECS（如果启用）

ecs:
  enable: true                     # 全局 ECS 开关
  default_ipv4: "8.8.8.8"          # 默认 IPv4 ECS
  default_ipv6: "2001:4860:4860::8888"
  ipv4_prefix: 24                  # ECS 前缀长度
  ipv6_prefix: 48
```

逻辑：
- 如果组配置了 `ecs_ip`，使用该值
- 如果组是 `proxy_ecs` 且未配置 `ecs_ip`，且全局 ECS 启用，使用全局默认值
- 否则不添加 ECS

### 缓存

#### DNS 缓存

RR 级别缓存，每条记录独立存储，包含：
- RR 记录内容
- 原始 TTL 和存储时间
- Rcode, AD, RA 标志

支持后端：
- **Redis** - 跨进程共享，持久化
- **Memory** - 进程内存，重启丢失

最大 TTL 固定为 24 小时。

#### 域名分类缓存

存储域名到分类的映射（如 `google.com -> proxy_site`）。

支持后端：
- **Redis** - TTL 可配置（默认 1 天）
- **Memory** - 无 TTL 限制

### 代理支持

支持通过 SOCKS5 代理进行：
- **上游 DNS 查询** - DoH (DNS-over-HTTPS) 和 TCP 协议
- **文件下载** - dlc.dat, Country.mmdb, GeoLite2-ASN.mmdb

每个上游组可以指定不同的 outbound：

```yaml
upstream_group:
  direct:
    nameservers: ["223.5.5.5"]
    outbound: "direct"             # 不使用代理
  proxy:
    nameservers: ["https://dns.google/dns-query"]
    outbound: "proxy"              # 使用 SOCKS5 代理

outbound:
  - tag: "proxy"
    type: "socks5"
    enable: true
    server: "127.0.0.1"
    port: 1080
    username: ""                   # 可选
    password: ""                   # 可选
  - tag: "file_download"           # 文件下载专用代理
    type: "socks5"
    enable: true
    server: "127.0.0.1"
    port: 1080
```

### Bootstrap DNS

用于解析上游 DNS 服务器的域名（如 `dns.google`）：

```yaml
bootstrap:
  nameservers: ["223.5.5.5", "119.29.29.29"]

upstream_group:
  proxy:
    nameservers: ["https://dns.google/dns-query"]  # 需要 bootstrap 解析 dns.google
    resolve_nameservers: ["223.5.5.5"]             # 可选，覆盖全局 bootstrap
    resolve_strategy: "ipv4_only"                  # ipv4_only, ipv6_only, prefer_ipv4, prefer_ipv6
```

### 自动更新

支持定时更新域名分类和 GeoIP 数据库（cron 表达式）：

```yaml
category_policy:
  preload:
    file: "https://github.com/v2fly/domain-list-community/releases/download/20231201/dlc.dat"
    update: "0 3 * * *"            # 每天凌晨 3 点更新

fallback:
  geoip: "https://github.com/Loyalsoldier/geoip/releases/latest/download/Country.mmdb"
  asn: "https://github.com/P3TERX/GeoLite.mmdb/releases/latest/download/GeoLite2-ASN.mmdb"
  update: "0 4 * * 0"              # 每周日凌晨 4 点更新
```

### 日志

支持结构化日志和自动轮转：

```yaml
log:
  level: "info"                    # debug, info, warn, error
  format: "json"                   # json, text
  output: "violet-dns.log"         # stdout 或文件路径
  max_size: 100                    # 单文件最大大小（MB）
  max_age: 7                       # 保留天数
  max_backups: 10                  # 保留文件数
  compress: true                   # 压缩旧日志
  total_size_limit: 1000           # 总大小限制（MB）
```

## 配置示例

### 完整配置

见 `run/config.yaml`，包含所有配置项和注释。

### 常见场景

#### 1. 国内直连 + 国外代理

```yaml
query_policy:
  - name: "cn_site"
    group: "direct"
  - name: "proxy_site"
    group: "proxy"
    options:
      disable_https: true          # 禁用 HTTPS/SVCB 记录
  - name: "block_site"
    group: "block"

category_policy:
  preload:
    domain_group:
      cn_site: ["cn", "apple-cn", "geolocation-cn"]
      proxy_site: ["google", "youtube", "facebook", "twitter", "github"]
      block_site: ["category-ads-all"]
```

#### 2. 自动分流

```yaml
query_policy:
  - name: "cn_site"
    group: "direct"
  - name: "proxy_site"
    group: "proxy"
    options:
      expected_ips: ["geoip:!cn"]  # 期望返回非国内 IP
      fallback_group: "direct"     # 如果是国内 IP，走直连
```

未分类域名自动使用 `proxy_ecs_fallback` 策略：
- 并发查询 proxy_ecs 和 proxy
- 如果 proxy_ecs 返回国内 IP，自动切换到 direct
- 自动学习域名分类

#### 3. 纯直连 + 广告过滤

```yaml
upstream_group:
  direct:
    nameservers: ["223.5.5.5", "119.29.29.29"]

query_policy:
  - name: "block_site"
    group: "block"

category_policy:
  preload:
    domain_group:
      block_site: ["category-ads-all"]
```

## 构建

### 标准构建

```bash
go build
```

### 交叉编译（OpenWrt）

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build
```

### 优化构建

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -trimpath
```

## 部署

### Systemd 服务

```ini
[Unit]
Description=violet-dns
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/violet-dns -d /etc/violet-dns
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

### Docker

```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY . .
RUN go build -ldflags="-s -w" -o violet-dns

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /build/violet-dns /usr/local/bin/
WORKDIR /etc/violet-dns
EXPOSE 53/udp 53/tcp
CMD ["violet-dns"]
```

## 性能优化

- **Singleflight** - 自动去重相同的并发查询
- **连接池** - Redis 和 HTTP 连接复用
- **并发查询** - proxy_ecs_fallback 策略并发查询多个上游
- **部分缓存** - CNAME 链部分命中减少上游查询
- **异步写入** - 域名分类缓存异步写入

## 故障排除

### Redis 连接失败

如果 Redis 配置错误或无法连接，程序会自动降级到内存缓存并继续运行。

### 文件下载失败

检查 `file_download` outbound 配置，确保代理可用。程序会在启动时测试代理连接。

### 域名分类不生效

1. 检查 `dlc.dat` 是否下载成功
2. 检查 `category_policy.preload.domain_group` 配置
3. 启用 debug 日志查看分类匹配结果

### 查询超时

调整超时配置（固定在代码中）：
- Redis 操作：5s
- 上游 DNS 查询：5s
- proxy_ecs_fallback 并发查询：3s

## 许可

MIT
