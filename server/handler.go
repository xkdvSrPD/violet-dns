package server

import (
	"github.com/miekg/dns"
	"violet-dns/middleware"
)

// Handler DNS 查询处理器
type Handler struct {
	logger *middleware.Logger
}

// NewHandler 创建新的处理器
func NewHandler(logger *middleware.Logger) *Handler {
	return &Handler{
		logger: logger,
	}
}

// ServeDNS 处理 DNS 查询
func (h *Handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	if len(r.Question) == 0 {
		return
	}

	q := r.Question[0]
	h.logger.Debug("收到查询: %s %s", q.Name, dns.TypeToString[q.Qtype])

	// 创建响应
	m := new(dns.Msg)
	m.SetReply(r)

	// 写入响应
	if err := w.WriteMsg(m); err != nil {
		h.logger.Error("写入响应失败: %v", err)
	}
}
