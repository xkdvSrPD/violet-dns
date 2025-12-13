package middleware

import (
	"github.com/miekg/dns"
	"golang.org/x/sync/singleflight"
)

// Singleflight 查询去重中间件
type Singleflight struct {
	group singleflight.Group
}

// NewSingleflight 创建查询去重中间件
func NewSingleflight() *Singleflight {
	return &Singleflight{}
}

// Do 执行去重查询
func (s *Singleflight) Do(key string, fn func() (*dns.Msg, error)) (*dns.Msg, error) {
	result, err, _ := s.group.Do(key, func() (interface{}, error) {
		return fn()
	})

	if err != nil {
		return nil, err
	}

	return result.(*dns.Msg), nil
}
