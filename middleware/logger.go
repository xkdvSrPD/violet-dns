package middleware

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/miekg/dns"
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

// ContextKey 用于在 context 中存储 trace_id
type ContextKey string

const TraceIDKey ContextKey = "trace_id"

// 颜色常量
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorGray   = "\033[90m"
)

// CustomJSONFormatter 自定义 JSON 格式化器，确保字段顺序
type CustomJSONFormatter struct {
	TimestampFormat string
}

// Format 实现 logrus.Formatter 接口
func (f *CustomJSONFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	var b bytes.Buffer

	// 设置时间格式
	timestampFormat := f.TimestampFormat
	if timestampFormat == "" {
		timestampFormat = "2006-01-02 15:04:05.000"
	}

	b.WriteString("{")

	// 1. time 字段（第一个）
	b.WriteString(fmt.Sprintf(`"time":"%s"`, entry.Time.Format(timestampFormat)))

	// 2. level 字段（第二个）
	b.WriteString(fmt.Sprintf(`,"level":"%s"`, entry.Level.String()))

	// 3. msg 字段（第三个）
	b.WriteString(fmt.Sprintf(`,"msg":"%s"`, escapeJSON(entry.Message)))

	// 4. 其他字段按字母顺序排序
	if len(entry.Data) > 0 {
		keys := make([]string, 0, len(entry.Data))
		for k := range entry.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			v := entry.Data[k]
			b.WriteString(fmt.Sprintf(`,"%s":`, escapeJSON(k)))

			switch val := v.(type) {
			case string:
				b.WriteString(fmt.Sprintf(`"%s"`, escapeJSON(val)))
			case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
				b.WriteString(fmt.Sprintf(`%d`, val))
			case float32, float64:
				b.WriteString(fmt.Sprintf(`%f`, val))
			case bool:
				b.WriteString(fmt.Sprintf(`%t`, val))
			case []string:
				b.WriteString("[")
				for i, s := range val {
					if i > 0 {
						b.WriteString(",")
					}
					b.WriteString(fmt.Sprintf(`"%s"`, escapeJSON(s)))
				}
				b.WriteString("]")
			default:
				b.WriteString(fmt.Sprintf(`"%v"`, val))
			}
		}
	}

	b.WriteString("}\n")
	return b.Bytes(), nil
}

// escapeJSON 转义 JSON 字符串
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

// CustomTextFormatter 自定义文本格式化器，带颜色支持
type CustomTextFormatter struct {
	TimestampFormat string
	ForceColors     bool
	DisableColors   bool
}

// Format 实现 logrus.Formatter 接口
func (f *CustomTextFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	var b bytes.Buffer

	timestampFormat := f.TimestampFormat
	if timestampFormat == "" {
		timestampFormat = "2006-01-02 15:04:05.000"
	}

	useColors := f.ForceColors || (!f.DisableColors && checkIfTerminal())

	// 时间
	if useColors {
		b.WriteString(colorGray)
	}
	b.WriteString(entry.Time.Format(timestampFormat))
	if useColors {
		b.WriteString(colorReset)
	}
	b.WriteString(" ")

	// 级别（带颜色）
	levelText := strings.ToUpper(entry.Level.String())
	if useColors {
		switch entry.Level {
		case logrus.DebugLevel:
			b.WriteString(colorBlue)
		case logrus.WarnLevel:
			b.WriteString(colorYellow)
		case logrus.ErrorLevel, logrus.FatalLevel, logrus.PanicLevel:
			b.WriteString(colorRed)
		}
	}
	b.WriteString(fmt.Sprintf("[%-5s]", levelText))
	if useColors {
		b.WriteString(colorReset)
	}
	b.WriteString(" ")

	// 消息
	b.WriteString(entry.Message)

	// 其他字段
	if len(entry.Data) > 0 {
		b.WriteString(" ")
		keys := make([]string, 0, len(entry.Data))
		for k := range entry.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		first := true
		for _, k := range keys {
			if !first {
				b.WriteString(" ")
			}
			first = false

			v := entry.Data[k]
			if useColors {
				b.WriteString(colorGray)
			}
			b.WriteString(fmt.Sprintf("%s=", k))
			if useColors {
				b.WriteString(colorReset)
			}

			switch val := v.(type) {
			case string:
				b.WriteString(val)
			case []string:
				b.WriteString("[" + strings.Join(val, ",") + "]")
			default:
				b.WriteString(fmt.Sprintf("%v", val))
			}
		}
	}

	b.WriteString("\n")
	return b.Bytes(), nil
}

// checkIfTerminal 检查是否是终端
func checkIfTerminal() bool {
	if fileInfo, _ := os.Stdout.Stat(); (fileInfo.Mode() & os.ModeCharDevice) != 0 {
		return true
	}
	return false
}

// Logger 日志中间件
type Logger struct {
	log    *logrus.Logger
	level  string
	closer io.Closer // 用于关闭文件句柄
}

// LogConfig 日志配置（简化版本）
type LogConfig struct {
	Level          string
	Format         string
	Output         string
	MaxSize        int
	MaxAge         int
	MaxBackups     int
	Compress       bool
	TotalSizeLimit int
}

// NewLogger 创建日志中间件
func NewLogger(cfg *LogConfig) *Logger {
	log := logrus.New()

	// 设置日志级别
	switch cfg.Level {
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
	if cfg.Format == "json" {
		log.SetFormatter(&CustomJSONFormatter{
			TimestampFormat: "2006-01-02 15:04:05.000",
		})
	} else {
		// text 格式，支持颜色
		isTerminal := cfg.Output == "" || cfg.Output == "stdout"
		log.SetFormatter(&CustomTextFormatter{
			TimestampFormat: "2006-01-02 15:04:05.000",
			ForceColors:     isTerminal,  // 输出到终端时强制启用颜色
			DisableColors:   !isTerminal, // 输出到文件时禁用颜色
		})
	}

	var closer io.Closer

	// 设置输出
	if cfg.Output == "" || cfg.Output == "stdout" {
		log.SetOutput(os.Stdout)
	} else {
		// 确保日志目录存在
		logDir := filepath.Dir(cfg.Output)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "创建日志目录失败: %v\n", err)
			log.SetOutput(os.Stdout)
		} else {
			// 使用 lumberjack 进行日志轮转
			lumberjackLogger := &lumberjack.Logger{
				Filename:   cfg.Output,
				MaxSize:    cfg.MaxSize,    // MB
				MaxAge:     cfg.MaxAge,     // days
				MaxBackups: cfg.MaxBackups, // 保留文件数
				Compress:   cfg.Compress,   // 是否压缩
			}
			log.SetOutput(lumberjackLogger)
			closer = lumberjackLogger

			// 如果配置了总大小限制，启动后台清理任务
			if cfg.TotalSizeLimit > 0 {
				go cleanupOldLogs(cfg.Output, cfg.TotalSizeLimit, lumberjackLogger)
			}
		}
	}

	return &Logger{
		log:    log,
		level:  cfg.Level,
		closer: closer,
	}
}

// Close 关闭日志文件句柄
func (l *Logger) Close() error {
	if l.closer != nil {
		return l.closer.Close()
	}
	return nil
}

// cleanupOldLogs 后台清理旧日志文件以满足总大小限制
func cleanupOldLogs(logFile string, totalSizeLimitMB int, logger *lumberjack.Logger) {
	ticker := time.NewTicker(1 * time.Hour) // 每小时检查一次
	defer ticker.Stop()

	for range ticker.C {
		// 获取日志目录
		logDir := filepath.Dir(logFile)
		baseName := filepath.Base(logFile)

		// 查找所有相关的日志文件
		files, err := filepath.Glob(filepath.Join(logDir, baseName+"*"))
		if err != nil {
			continue
		}

		// 计算总大小
		var totalSize int64
		type fileInfo struct {
			path    string
			size    int64
			modTime time.Time
		}
		var fileInfos []fileInfo

		for _, file := range files {
			info, err := os.Stat(file)
			if err != nil {
				continue
			}
			totalSize += info.Size()
			fileInfos = append(fileInfos, fileInfo{
				path:    file,
				size:    info.Size(),
				modTime: info.ModTime(),
			})
		}

		// 如果超过总大小限制，删除最旧的文件
		limitBytes := int64(totalSizeLimitMB) * 1024 * 1024
		if totalSize > limitBytes {
			// 按修改时间排序（旧的在前）
			for i := 0; i < len(fileInfos)-1; i++ {
				for j := i + 1; j < len(fileInfos); j++ {
					if fileInfos[i].modTime.After(fileInfos[j].modTime) {
						fileInfos[i], fileInfos[j] = fileInfos[j], fileInfos[i]
					}
				}
			}

			// 删除最旧的文件直到满足大小限制
			for _, fi := range fileInfos {
				if totalSize <= limitBytes {
					break
				}
				// 不删除当前正在使用的日志文件
				if fi.path == logFile {
					continue
				}
				if err := os.Remove(fi.path); err == nil {
					totalSize -= fi.size
				}
			}
		}
	}
}

// NewTraceID 生成新的 trace_id
func NewTraceID() string {
	return uuid.New().String()[:8] // 使用前8个字符，保持简洁
}

// WithTraceID 创建包含 trace_id 的 context
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, TraceIDKey, traceID)
}

// GetTraceID 从 context 获取 trace_id
func GetTraceID(ctx context.Context) string {
	if traceID, ok := ctx.Value(TraceIDKey).(string); ok {
		return traceID
	}
	return "unknown"
}

// withTraceID 为日志添加 trace_id
func (l *Logger) withTraceID(ctx context.Context) *logrus.Entry {
	return l.log.WithField("trace_id", GetTraceID(ctx))
}

// =============================================================================
// 系统启动和通用日志
// =============================================================================

// Info 记录 info 日志（无 trace_id 的系统日志）
func (l *Logger) Info(format string, args ...interface{}) {
	l.log.Infof(format, args...)
}

// Debug 记录 debug 日志（无 trace_id 的系统日志）
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log.Debugf(format, args...)
}

// Warn 记录 warn 日志（无 trace_id 的系统日志）
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log.Warnf(format, args...)
}

// Error 记录 error 日志（无 trace_id 的系统日志）
func (l *Logger) Error(format string, args ...interface{}) {
	l.log.Errorf(format, args...)
}

// =============================================================================
// DNS 查询相关日志（带 trace_id）
// =============================================================================

// LogQueryStart 记录查询开始
func (l *Logger) LogQueryStart(ctx context.Context, clientIP, domain string, qtype uint16) {
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":     "query_start",
		"client_ip": clientIP,
		"domain":    domain,
		"qtype":     dns.TypeToString[qtype],
	}).Debug("查询开始")
}

// LogQueryComplete 记录查询完成（INFO 级别 - 必须记录）
func (l *Logger) LogQueryComplete(ctx context.Context, domain string, qtype, rcode uint16, cached bool, latency time.Duration, upstream string, answerCount int) {
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":        "query_complete",
		"domain":       domain,
		"qtype":        dns.TypeToString[qtype],
		"rcode":        dns.RcodeToString[int(rcode)],
		"cached":       cached,
		"latency_ms":   latency.Milliseconds(),
		"upstream":     upstream,
		"answer_count": answerCount,
	}).Info("查询完成")
}

// LogCacheHit 记录缓存命中
func (l *Logger) LogCacheHit(ctx context.Context, domain string, qtype uint16, remainingTTL time.Duration) {
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":         "cache_hit",
		"domain":        domain,
		"qtype":         dns.TypeToString[qtype],
		"remaining_ttl": remainingTTL.Seconds(),
	}).Debug("缓存命中")
}

// LogCacheMiss 记录缓存未命中
func (l *Logger) LogCacheMiss(ctx context.Context, domain string, qtype uint16) {
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":  "cache_miss",
		"domain": domain,
		"qtype":  dns.TypeToString[qtype],
	}).Debug("缓存未命中")
}

// LogCacheSet 记录缓存写入
func (l *Logger) LogCacheSet(ctx context.Context, domain string, qtype uint16, ttl time.Duration) {
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":   "cache_set",
		"domain":  domain,
		"qtype":   dns.TypeToString[qtype],
		"ttl_sec": ttl.Seconds(),
	}).Debug("缓存写入")
}

// =============================================================================
// 域名分类和策略匹配
// =============================================================================

// LogCategoryMatch 记录域名分类匹配
func (l *Logger) LogCategoryMatch(ctx context.Context, domain, category string, matched bool) {
	event := "category_matched"
	if !matched {
		event = "category_not_matched"
		category = "unknown"
	}
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":    event,
		"domain":   domain,
		"category": category,
	}).Debug("分类匹配")
}

// LogPolicyMatch 记录策略匹配
func (l *Logger) LogPolicyMatch(ctx context.Context, domain, policyName, upstreamGroup string) {
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":          "policy_matched",
		"domain":         domain,
		"policy":         policyName,
		"upstream_group": upstreamGroup,
	}).Debug("策略匹配")
}

// LogPolicyOptions 记录策略选项
func (l *Logger) LogPolicyOptions(ctx context.Context, domain string, options map[string]interface{}) {
	if len(options) == 0 {
		return
	}
	fields := logrus.Fields{
		"event":  "policy_options",
		"domain": domain,
	}
	for k, v := range options {
		fields[k] = v
	}
	l.withTraceID(ctx).WithFields(fields).Debug("策略选项")
}

// =============================================================================
// 上游查询
// =============================================================================

// LogUpstreamQuery 记录上游查询开始
func (l *Logger) LogUpstreamQuery(ctx context.Context, domain string, qtype uint16, upstreamGroup string, nameservers []string) {
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":          "upstream_query_start",
		"domain":         domain,
		"qtype":          dns.TypeToString[qtype],
		"upstream_group": upstreamGroup,
		"nameservers":    nameservers,
	}).Debug("上游查询开始")
}

// LogUpstreamResponse 记录上游响应
func (l *Logger) LogUpstreamResponse(ctx context.Context, domain string, qtype uint16, nameserver string, rcode uint16, answerCount int, latency time.Duration) {
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":        "upstream_response",
		"domain":       domain,
		"qtype":        dns.TypeToString[qtype],
		"nameserver":   nameserver,
		"rcode":        dns.RcodeToString[int(rcode)],
		"answer_count": answerCount,
		"latency_ms":   latency.Milliseconds(),
	}).Debug("上游响应")
}

// LogUpstreamError 记录上游查询失败
func (l *Logger) LogUpstreamError(ctx context.Context, domain, nameserver string, err error, latency time.Duration) {
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":      "upstream_error",
		"domain":     domain,
		"nameserver": nameserver,
		"error":      err.Error(),
		"latency_ms": latency.Milliseconds(),
	}).Debug("上游查询失败")
}

// =============================================================================
// DNS 应答和 IP 验证
// =============================================================================

// LogDNSAnswer 记录 DNS 应答
func (l *Logger) LogDNSAnswer(ctx context.Context, domain string, answers []dns.RR) {
	if len(answers) == 0 {
		return
	}

	// 提取 IP 地址
	ips := make([]string, 0)
	answerStrs := make([]string, 0, len(answers))

	for _, ans := range answers {
		answerStrs = append(answerStrs, ans.String())

		// 提取 A 和 AAAA 记录的 IP
		switch rr := ans.(type) {
		case *dns.A:
			ips = append(ips, rr.A.String())
		case *dns.AAAA:
			ips = append(ips, rr.AAAA.String())
		}
	}

	fields := logrus.Fields{
		"event":   "dns_answer",
		"domain":  domain,
		"answers": answerStrs,
	}

	// 如果有 IP 地址，单独列出
	if len(ips) > 0 {
		fields["ips"] = ips
	}

	l.withTraceID(ctx).WithFields(fields).Debug("DNS应答")
}

// LogIPValidation 记录 IP 验证
func (l *Logger) LogIPValidation(ctx context.Context, domain string, ips []string, expectedIPs []string, passed bool) {
	result := "passed"
	if !passed {
		result = "failed"
	}
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":        "ip_validation",
		"domain":       domain,
		"ips":          ips,
		"expected_ips": expectedIPs,
		"result":       result,
	}).Debug("IP验证")
}

// =============================================================================
// 策略回退
// =============================================================================

// LogFallback 记录策略回退（INFO 级别）
func (l *Logger) LogFallback(ctx context.Context, domain, from, to, reason string) {
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":  "fallback",
		"domain": domain,
		"from":   from,
		"to":     to,
		"reason": reason,
	}).Info("策略回退")
}

// LogFallbackDetail 记录回退详情
func (l *Logger) LogFallbackDetail(ctx context.Context, domain, from, to, reason string, additionalInfo map[string]interface{}) {
	fields := logrus.Fields{
		"event":  "fallback_detail",
		"domain": domain,
		"from":   from,
		"to":     to,
		"reason": reason,
	}
	for k, v := range additionalInfo {
		fields[k] = v
	}
	l.withTraceID(ctx).WithFields(fields).Debug("回退详情")
}

// =============================================================================
// ProxyECSFallback 策略
// =============================================================================

// LogProxyECSFallback 记录 proxy_ecs_fallback 策略执行
func (l *Logger) LogProxyECSFallback(ctx context.Context, domain, step string, details map[string]interface{}) {
	fields := logrus.Fields{
		"event":  "proxy_ecs_fallback",
		"domain": domain,
		"step":   step,
	}
	for k, v := range details {
		fields[k] = v
	}
	l.withTraceID(ctx).WithFields(fields).Debug("ProxyECSFallback")
}

// =============================================================================
// Block 策略
// =============================================================================

// LogBlock 记录阻止策略（INFO 级别）
func (l *Logger) LogBlock(ctx context.Context, domain string, qtype uint16, blockType string) {
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":      "block",
		"domain":     domain,
		"qtype":      dns.TypeToString[qtype],
		"block_type": blockType,
	}).Info("域名已阻止")
}

// =============================================================================
// 错误处理
// =============================================================================

// LogError 记录错误
func (l *Logger) LogError(ctx context.Context, event, domain string, err error, additionalInfo map[string]interface{}) {
	fields := logrus.Fields{
		"event":  event,
		"domain": domain,
		"error":  err.Error(),
	}
	for k, v := range additionalInfo {
		fields[k] = v
	}
	l.withTraceID(ctx).WithFields(fields).Error("错误")
}

// LogQueryError 记录查询错误（ERROR 级别 - 必须记录）
func (l *Logger) LogQueryError(ctx context.Context, clientIP, domain string, err error) {
	l.withTraceID(ctx).WithFields(logrus.Fields{
		"event":     "query_error",
		"client_ip": clientIP,
		"domain":    domain,
		"error":     err.Error(),
	}).Error("查询失败")
}

// =============================================================================
// 工具函数
// =============================================================================

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
