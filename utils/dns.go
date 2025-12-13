package utils

import (
	"net"

	"github.com/miekg/dns"
)

// ExtractIPs 从 DNS 应答中提取 IP 地址
func ExtractIPs(answers []dns.RR) []net.IP {
	var ips []net.IP
	for _, rr := range answers {
		switch record := rr.(type) {
		case *dns.A:
			ips = append(ips, record.A)
		case *dns.AAAA:
			ips = append(ips, record.AAAA)
		}
	}
	return ips
}

// HasAnswer 检查 DNS 响应是否有应答
func HasAnswer(msg *dns.Msg) bool {
	return msg != nil && len(msg.Answer) > 0
}

// IsNXDomain 检查是否是 NXDOMAIN 响应
func IsNXDomain(msg *dns.Msg) bool {
	return msg != nil && msg.Rcode == dns.RcodeNameError
}

// CreateNXDomainResponse 创建 NXDOMAIN 响应
func CreateNXDomainResponse(query *dns.Msg) *dns.Msg {
	msg := new(dns.Msg)
	msg.SetReply(query)
	msg.Rcode = dns.RcodeNameError
	return msg
}

// CreateNoErrorResponse 创建 NOERROR 响应（无应答）
func CreateNoErrorResponse(query *dns.Msg) *dns.Msg {
	msg := new(dns.Msg)
	msg.SetReply(query)
	msg.Rcode = dns.RcodeSuccess
	return msg
}

// CreateBlockedResponse 创建被阻止的响应（返回 0.0.0.0）
func CreateBlockedResponse(query *dns.Msg) *dns.Msg {
	msg := new(dns.Msg)
	msg.SetReply(query)
	msg.Rcode = dns.RcodeSuccess

	for _, q := range query.Question {
		switch q.Qtype {
		case dns.TypeA:
			rr := &dns.A{
				Hdr: dns.RR_Header{
					Name:   q.Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    60,
				},
				A: net.IPv4zero,
			}
			msg.Answer = append(msg.Answer, rr)
		case dns.TypeAAAA:
			rr := &dns.AAAA{
				Hdr: dns.RR_Header{
					Name:   q.Name,
					Rrtype: dns.TypeAAAA,
					Class:  dns.ClassINET,
					Ttl:    60,
				},
				AAAA: net.IPv6zero,
			}
			msg.Answer = append(msg.Answer, rr)
		}
	}

	return msg
}
