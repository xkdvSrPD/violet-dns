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
	port         int
	bind         string
	router       *router.Router
	logger       *middleware.Logger
	singleflight *middleware.Singleflight
}

// NewServer 创建新的 DNS 服务器
func NewServer(port int, bind string, r *router.Router, logger *middleware.Logger, sf *middleware.Singleflight) *Server {
	return &Server{
		port:         port,
		bind:         bind,
		router:       r,
		logger:       logger,
		singleflight: sf,
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

	// DEBUG: 记录收到查询请求
	s.logger.Debug("收到DNS查询: client=%s domain=%s qtype=%s", clientIP, domain, dns.TypeToString[q.Qtype])

	// 使用 singleflight 去重
	key := fmt.Sprintf("%s:%d", domain, q.Qtype)
	resp, err := s.singleflight.Do(key, func() (*dns.Msg, error) {
		return s.router.Route(context.Background(), domain, q.Qtype)
	})

	if err != nil {
		s.logger.Error("查询失败: client=%s domain=%s error=%v", clientIP, domain, err)

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

	// DEBUG: 记录返回响应
	s.logger.Debug("返回DNS响应: client=%s domain=%s rcode=%s answer_count=%d",
		clientIP, domain, dns.RcodeToString[resp.Rcode], len(resp.Answer))

	// 写入响应
	if err := w.WriteMsg(resp); err != nil {
		s.logger.Error("写入响应失败: client=%s error=%v", clientIP, err)
	}
}
