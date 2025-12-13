# 实现一个DNS服务器
详细设计如下

## port
port为DNS服务器监听端口，支持接收udp查询请求

## init_nameserver
用于解析配置文件中的所有域名

## upstream_group
按照配置文件，将DNS服务器分为3组
- proxy
- proxy_ecs
- direct
每一组服务器支持https和udp查询，对于system进行处理，直接使用系统的DNS服务器
在查询DNS时，每组中的DNS服务器并发查询，去最快返回的为本组的结果
对于proxy_ecs的组，要强制添加ECS，ECS地址需要配置

outbound 为此dns查询发出使用的代理，如果配置了则使用，否则使用默认策略direct

## socks5
socks代理版本为socks5, 如果启用了socks代理，则将proxy和proxy_ecs的查询请求使用socks代理发出

## ecs
对于proxy的服务器需要添加ecs，ecs地址从配置文件中获取

## cache
如果启用了clear为true，则在启动的时候清空redis中所有数据

### dns_cache
DNS查询返回结果的缓存
如果启用了缓存，严格设置缓存ttl为DNS查询结果的ttl
否则不使用缓存，所有的查询都必须重新调用上游服务器查询

### category_cache
域名分类的缓存
将会从dlc文件中加载或对于不存在当前分类的域名后期更新

# fallback
此处只对query_policy为proxy_ecs_fallback的策略生效
使用proxy_ecs_fallback策略的情况下，需要同时查询proxy_ecs和proxy这两组的dns服务器
必须等待proxy_ecs返回结果，如果返回的ip不存在于rule当中，则fallback到proxy，使用proxy返回的结果
同时将数据写入缓存中，标记域名分组为proxy_site
如果返回的ip有任意一个IP存在于rule中，则fallback到direct，使用direct组中的服务器重新进行DNS解析
同时将数据写入缓存中，标记此域名为direct_site

# category_policy
如果设置了preload，则需要预先填充域名分组数据，分类数据从file中下载文件，并加载到redis数据库中
update为定时更新的表达式，以当前系统时间为准执行，更新时需要对redis中的数据进行更新，如果未配置此项目则不进行更新
需要将域名分为domain_group，配置了多少个则分多少个组，每组的域名从文件中读取指定的分类并加载到缓存中

# query_policy
此处为域名分组和该分组的查询策略
每个分组的已经通过preload的方式加载，或分组完全为空，通过后将查询后更新的方式补充
当有dns查询请求发送到系统是，需要先对域名进行匹配分组，从上到下以此匹配，匹配到之后使用配置upstream_group进行查询
注意查询时的规则，需要按照配置的options对查询结果进行过滤，可以配置ecs，此处的配置优先级高于全局配置
对于所有匹配失败的全部落入unknown中，使用指定的策略进行查询proxy_ecs_fallback已在上文中解释

# log
debug时需要输出所有的详细信息，每次redis存储、dns查询，是否fallback，都需要记录，保存日志格式简介可观测
info时，只输出将要信息，但是需要包括查询域名，返回类型，是否fallback，查询耗时，是否使用了缓存


# API设计
暂不考虑

# 配置文件校验

## port
校验端口是否被占用

## upstream_group
必须配置proxy、proxy_ecs、direct，且每个组至少有1个dns服务器
指定的outbound必须存在，direct默认存在

## outbound
暂时只支持type为socks5代理，默认添加direct出站

## ecs
必须配置，格式必须符合ecs格式

## cache
可选配置，默认启用cache，如果为配置type为redis，则使用内存缓存

## redis
可选配置，未指定database则默认使用0

## category_policy
preload启用时，必须配置file，可以为文件地址或url
update可选配置，未配置则不进行自动更新
domain_group必须配置可以为空，但是域名分组必须存在于dlc文件中

## query_policy
名称必须和domain_group相同
group必须存在于upstream_group中，upstream_group预置block策略，block_type为匹配到改域名时的返回值类型
如果没有配置unknown，则自动添加unknown到最后

## fallback
geoip、asn 必须配置
update可以不配置
rule必须配置

## log
可以不配置，默认为info

# 启动
## 校验配置文件
## 加载当前配置到内存中
启动时需要将当前配置加载到内存中，注意此处不固定任何配置，方便后期开发外部api实时修改配置
配置文件仅作为启动时的默认配置，其中大部分配置都可通过API修改，暂不开发API

## 检查文件是否下载
默认文件名
默认下载到程序运行目录中。如果文件下载了不需要重新下载

## 执行preload


# 参考代码

/home/violet/Code/geoview/
/home/violet/Code/sing-box/
/home/violet/Code/Xray-core/
/home/violet/Code/mihomo/