package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/miekg/dns"
	"violet-dns/middleware"
	"violet-dns/router"
)

// Server DNS 服务器
type Server struct {
	port   int
	bind   string
	router router.QueryRouter // 使用接口而非具体类型
	logger *middleware.Logger
}

// NewServer 创建新的 DNS 服务器
func NewServer(port int, bind string, r router.QueryRouter, logger *middleware.Logger) *Server {
	return &Server{
		port:   port,
		bind:   bind,
		router: r,
		logger: logger,
	}
}

// Start 启动服务器
func (s *Server) Start(ctx context.Context) error {
	// 创建 DNS 处理器
	dns.HandleFunc(".", s.handleQuery)

	// 启动 UDP 服务器
	addr := fmt.Sprintf("%s:%d", s.bind, s.port)
	server := &dns.Server{
		Addr: addr,
		Net:  "udp",
	}

	s.logger.Info("DNS 服务器启动: %s", addr)

	// 在 goroutine 中启动服务器
	go func() {
		if err := server.ListenAndServe(); err != nil {
			s.logger.Error("DNS 服务器错误: %v", err)
		}
	}()

	// 等待上下文取消
	<-ctx.Done()

	// 优雅关闭
	s.logger.Info("正在关闭 DNS 服务器...")
	return server.Shutdown()
}

// handleQuery 处理 DNS 查询
func (s *Server) handleQuery(w dns.ResponseWriter, r *dns.Msg) {
	if len(r.Question) == 0 {
		return
	}

	q := r.Question[0]
	domain := strings.TrimSuffix(q.Name, ".")
	clientIP := w.RemoteAddr().String()

	// 生成 trace_id 并创建 context
	traceID := middleware.NewTraceID()
	ctx := middleware.WithTraceID(context.Background(), traceID)

	// DEBUG: 记录收到查询请求
	s.logger.LogQueryStart(ctx, clientIP, domain, q.Qtype)

	// 直接调用 router
	resp, err := s.router.Route(ctx, domain, q.Qtype)

	if err != nil {
		// ERROR: 记录查询失败
		s.logger.LogQueryError(ctx, clientIP, domain, err)

		// 返回 SERVFAIL
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeServerFailure
		w.WriteMsg(m)
		return
	}

	// 设置查询 ID
	resp.SetReply(r)
	resp.Id = r.Id

	// 写入响应
	if err := w.WriteMsg(resp); err != nil {
		s.logger.Error("写入响应失败: client=%s error=%v", clientIP, err)
	}
}
