package middleware

import (
	"fmt"
	"time"

	"github.com/miekg/dns"
	"github.com/sirupsen/logrus"
)

// Logger 日志中间件
type Logger struct {
	log   *logrus.Logger
	level string
}

// NewLogger 创建日志中间件
func NewLogger(level, format string) *Logger {
	log := logrus.New()

	// 设置日志级别
	switch level {
	case "debug":
		log.SetLevel(logrus.DebugLevel)
	case "info":
		log.SetLevel(logrus.InfoLevel)
	case "warn":
		log.SetLevel(logrus.WarnLevel)
	case "error":
		log.SetLevel(logrus.ErrorLevel)
	default:
		log.SetLevel(logrus.InfoLevel)
	}

	// 设置格式
	if format == "json" {
		log.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: "2006-01-02 15:04:05.000",
		})
	} else {
		log.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05.000",
		})
	}

	return &Logger{
		log:   log,
		level: level,
	}
}

// Info 记录 info 日志
func (l *Logger) Info(format string, args ...interface{}) {
	l.log.Infof(format, args...)
}

// Debug 记录 debug 日志
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log.Debugf(format, args...)
}

// Warn 记录 warn 日志
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log.Warnf(format, args...)
}

// Error 记录 error 日志
func (l *Logger) Error(format string, args ...interface{}) {
	l.log.Errorf(format, args...)
}

// LogQuery 记录查询日志（INFO 级别）
func (l *Logger) LogQuery(domain string, qtype, rcode uint16, cached bool, latency time.Duration, upstreamGroup string, answerCount int) {
	qtypeName := dns.TypeToString[qtype]
	rcodeName := dns.RcodeToString[int(rcode)]

	l.log.WithFields(logrus.Fields{
		"domain":       domain,
		"qtype":        qtypeName,
		"rcode":        rcodeName,
		"cached":       cached,
		"latency_ms":   latency.Milliseconds(),
		"upstream":     upstreamGroup,
		"answer_count": answerCount,
	}).Info("DNS查询完成")
}

// LogQueryStart 记录查询开始（DEBUG 级别）
func (l *Logger) LogQueryStart(domain string, qtype uint16) {
	qtypeName := dns.TypeToString[qtype]
	l.log.WithFields(logrus.Fields{
		"domain": domain,
		"qtype":  qtypeName,
	}).Debug("[查询开始]")
}

// LogCacheHit 记录缓存命中（DEBUG 级别）
func (l *Logger) LogCacheHit(domain string, qtype uint16, ttl time.Duration) {
	qtypeName := dns.TypeToString[qtype]
	l.log.WithFields(logrus.Fields{
		"domain":        domain,
		"qtype":         qtypeName,
		"remaining_ttl": ttl.Seconds(),
	}).Debug("[缓存命中]")
}

// LogCacheMiss 记录缓存未命中（DEBUG 级别）
func (l *Logger) LogCacheMiss(domain string, qtype uint16) {
	qtypeName := dns.TypeToString[qtype]
	l.log.WithFields(logrus.Fields{
		"domain": domain,
		"qtype":  qtypeName,
	}).Debug("[缓存未命中]")
}

// LogCategoryMatch 记录域名分类匹配（DEBUG 级别）
func (l *Logger) LogCategoryMatch(domain, category string, matched bool) {
	if matched {
		l.log.WithFields(logrus.Fields{
			"domain":   domain,
			"category": category,
		}).Debug("[分类匹配成功]")
	} else {
		l.log.WithFields(logrus.Fields{
			"domain": domain,
		}).Debug("[分类未匹配，使用 unknown]")
	}
}

// LogPolicyMatch 记录策略匹配（DEBUG 级别）
func (l *Logger) LogPolicyMatch(domain, policyName, upstreamGroup string, hasOptions bool) {
	fields := logrus.Fields{
		"domain":         domain,
		"policy":         policyName,
		"upstream_group": upstreamGroup,
	}
	l.log.WithFields(fields).Debug("[策略匹配]")
}

// LogPolicyOptions 记录策略选项（DEBUG 级别）
func (l *Logger) LogPolicyOptions(domain string, options map[string]interface{}) {
	if len(options) > 0 {
		l.log.WithFields(logrus.Fields{
			"domain":  domain,
			"options": options,
		}).Debug("[策略选项]")
	}
}

// LogUpstreamQuery 记录上游查询（DEBUG 级别）
func (l *Logger) LogUpstreamQuery(domain string, qtype uint16, upstreamGroup string, nameservers []string) {
	qtypeName := dns.TypeToString[qtype]
	l.log.WithFields(logrus.Fields{
		"domain":         domain,
		"qtype":          qtypeName,
		"upstream_group": upstreamGroup,
		"nameservers":    nameservers,
	}).Debug("[上游查询]")
}

// LogUpstreamResponse 记录上游响应（DEBUG 级别）
func (l *Logger) LogUpstreamResponse(domain string, qtype uint16, nameserver string, rcode uint16, answerCount int, latency time.Duration) {
	qtypeName := dns.TypeToString[qtype]
	rcodeName := dns.RcodeToString[int(rcode)]
	l.log.WithFields(logrus.Fields{
		"domain":       domain,
		"qtype":        qtypeName,
		"nameserver":   nameserver,
		"rcode":        rcodeName,
		"answer_count": answerCount,
		"latency_ms":   latency.Milliseconds(),
	}).Debug("[上游响应]")
}

// LogDNSAnswer 记录 DNS 应答详情（DEBUG 级别）
func (l *Logger) LogDNSAnswer(domain string, answers []dns.RR) {
	if len(answers) == 0 {
		return
	}

	answerStrs := make([]string, 0, len(answers))
	for _, ans := range answers {
		answerStrs = append(answerStrs, ans.String())
	}

	l.log.WithFields(logrus.Fields{
		"domain":  domain,
		"answers": answerStrs,
	}).Debug("[DNS应答]")
}

// LogIPValidation 记录 IP 验证（DEBUG 级别）
func (l *Logger) LogIPValidation(domain string, ips []string, expectedIPs []string, passed bool) {
	l.log.WithFields(logrus.Fields{
		"domain":       domain,
		"ips":          ips,
		"expected_ips": expectedIPs,
		"passed":       passed,
	}).Debug("[IP验证]")
}

// LogFallback 记录回退（INFO 级别）
func (l *Logger) LogFallback(domain, from, to, reason string) {
	l.log.WithFields(logrus.Fields{
		"domain": domain,
		"from":   from,
		"to":     to,
		"reason": reason,
	}).Info("策略回退")
}

// LogFallbackDetail 记录回退详情（DEBUG 级别）
func (l *Logger) LogFallbackDetail(domain, from, to, reason string, additionalInfo map[string]interface{}) {
	fields := logrus.Fields{
		"domain": domain,
		"from":   from,
		"to":     to,
		"reason": reason,
	}
	for k, v := range additionalInfo {
		fields[k] = v
	}
	l.log.WithFields(fields).Debug("[回退详情]")
}

// LogBlock 记录阻止策略（INFO 级别）
func (l *Logger) LogBlock(domain string, qtype uint16, blockType string) {
	qtypeName := dns.TypeToString[qtype]
	l.log.WithFields(logrus.Fields{
		"domain":     domain,
		"qtype":      qtypeName,
		"block_type": blockType,
	}).Info("域名已阻止")
}

// LogProxyECSFallback 记录 proxy_ecs_fallback 策略执行（DEBUG 级别）
func (l *Logger) LogProxyECSFallback(domain string, step string, details map[string]interface{}) {
	fields := logrus.Fields{
		"domain": domain,
		"step":   step,
	}
	for k, v := range details {
		fields[k] = v
	}
	l.log.WithFields(fields).Debug("[ProxyECSFallback]")
}

// LogCacheSet 记录缓存写入（DEBUG 级别）
func (l *Logger) LogCacheSet(domain string, qtype uint16, ttl time.Duration) {
	qtypeName := dns.TypeToString[qtype]
	l.log.WithFields(logrus.Fields{
		"domain":  domain,
		"qtype":   qtypeName,
		"ttl_sec": ttl.Seconds(),
	}).Debug("[缓存写入]")
}

// LogError 记录错误（ERROR 级别）
func (l *Logger) LogError(context, domain string, err error, additionalInfo map[string]interface{}) {
	fields := logrus.Fields{
		"context": context,
		"domain":  domain,
		"error":   err.Error(),
	}
	for k, v := range additionalInfo {
		fields[k] = v
	}
	l.log.WithFields(fields).Error("发生错误")
}

// ExtractIPsFromAnswer 从 Answer 中提取 IP 地址字符串
func ExtractIPsFromAnswer(answers []dns.RR) []string {
	ips := make([]string, 0)
	for _, ans := range answers {
		switch rr := ans.(type) {
		case *dns.A:
			ips = append(ips, rr.A.String())
		case *dns.AAAA:
			ips = append(ips, rr.AAAA.String())
		}
	}
	return ips
}

// FormatDuration 格式化时间间隔为易读格式
func FormatDuration(d time.Duration) string {
	if d < time.Microsecond {
		return fmt.Sprintf("%dns", d.Nanoseconds())
	} else if d < time.Millisecond {
		return fmt.Sprintf("%.2fμs", float64(d.Nanoseconds())/1000.0)
	} else if d < time.Second {
		return fmt.Sprintf("%.2fms", float64(d.Milliseconds()))
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
