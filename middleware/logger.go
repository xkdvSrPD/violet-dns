package middleware

import (
	"time"

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
		log.SetFormatter(&logrus.JSONFormatter{})
	} else {
		log.SetFormatter(&logrus.TextFormatter{})
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

// LogQuery 记录查询日志
func (l *Logger) LogQuery(domain string, qtype, rcode uint16, cached bool, latency time.Duration, upstreamGroup string) {
	l.Info("查询: domain=%s qtype=%d rcode=%d cached=%v latency=%v upstream=%s",
		domain, qtype, rcode, cached, latency, upstreamGroup)
}

// LogFallback 记录回退日志
func (l *Logger) LogFallback(domain, from, to, reason string) {
	l.Info("回退: domain=%s from=%s to=%s reason=%s", domain, from, to, reason)
}
