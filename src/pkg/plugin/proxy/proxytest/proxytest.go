// Package proxytest 提供测试用的假代理服务器：一个最小 HTTP 代理和一个最小 SOCKS5 服务器。
//
// 两者都不真正转发流量，而是自己扮演目标站点，对收到的 HTTP 请求回 204，
// 并记录代理侧看到的目标地址与认证信息，供断言"请求确实经过代理"。
// 仅用于测试，不访问任何外部网络。
package proxytest

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

// Seen 是代理侧记录的一次请求。
type Seen struct {
	// Target 是代理收到的目标：HTTP 代理为绝对 URL，SOCKS5 为 host:port。
	Target string
	// Domain 仅对 SOCKS5 有意义：目标是否以域名（而非 IP）形式交给了代理。
	Domain bool
	// User 是客户端提供的代理认证用户名，未认证为空。
	User string
	// Password 是客户端提供的代理认证密码。
	Password string
}

// Server 是假代理，Addr 为 host:port。
type Server struct {
	Addr string

	mu   sync.Mutex
	seen []Seen
	ln   net.Listener
}

// Seen 返回已记录请求的副本。
func (s *Server) Seen() []Seen {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Seen(nil), s.seen...)
}

func (s *Server) record(v Seen) {
	s.mu.Lock()
	s.seen = append(s.seen, v)
	s.mu.Unlock()
}

// NewHTTP 启动假 HTTP 代理（仅支持普通请求，不支持 CONNECT）。
func NewHTTP(t testing.TB) *Server {
	t.Helper()
	s := &Server{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		if ph := r.Header.Get("Proxy-Authorization"); ph != "" {
			req := &http.Request{Header: http.Header{"Authorization": {ph}}}
			u, p, _ = req.BasicAuth()
		}
		s.record(Seen{Target: r.URL.String(), User: u, Password: p})
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	s.Addr = srv.Listener.Addr().String()
	return s
}

// NewSocks5 启动假 SOCKS5 服务器。user 非空时要求用户名密码认证（RFC 1929），否则接受无认证。
func NewSocks5(t testing.TB, user, pass string) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Addr: ln.Addr().String(), ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c, user, pass)
		}
	}()
	return s
}

func (s *Server) serve(c net.Conn, user, pass string) {
	defer func() { _ = c.Close() }()
	br := bufio.NewReader(c)
	gotUser, gotPass, ok := socksHandshake(br, c, user, pass)
	if !ok {
		return
	}
	// 请求：VER CMD RSV ATYP ADDR PORT
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(br, hdr); err != nil || hdr[1] != 1 {
		return
	}
	var host string
	domain := false
	switch hdr[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3:
		n, err := br.ReadByte()
		if err != nil {
			return
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host, domain = string(b), true
	default:
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(br, pb); err != nil {
		return
	}
	s.record(Seen{
		Target: net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb)))),
		Domain: domain, User: gotUser, Password: gotPass,
	})
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	// 扮演目标站点：读一个 HTTP 请求，回 204。
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	_ = req.Body.Close()
	_, _ = io.WriteString(c, "HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")
}

// socksHandshake 完成方法协商与可选的用户名密码认证。
func socksHandshake(br *bufio.Reader, c net.Conn, user, pass string) (gotUser, gotPass string, ok bool) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(br, h); err != nil || h[0] != 5 {
		return "", "", false
	}
	methods := make([]byte, h[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return "", "", false
	}
	want := byte(0)
	if user != "" {
		want = 2
	}
	found := false
	for _, m := range methods {
		if m == want {
			found = true
		}
	}
	if !found {
		_, _ = c.Write([]byte{5, 0xff})
		return "", "", false
	}
	if _, err := c.Write([]byte{5, want}); err != nil {
		return "", "", false
	}
	if want == 0 {
		return "", "", true
	}
	ver, err := br.ReadByte()
	if err != nil || ver != 1 {
		return "", "", false
	}
	ul, _ := br.ReadByte()
	ub := make([]byte, ul)
	if _, err := io.ReadFull(br, ub); err != nil {
		return "", "", false
	}
	pl, _ := br.ReadByte()
	pb := make([]byte, pl)
	if _, err := io.ReadFull(br, pb); err != nil {
		return "", "", false
	}
	if string(ub) != user || string(pb) != pass {
		_, _ = c.Write([]byte{1, 1})
		return "", "", false
	}
	_, _ = c.Write([]byte{1, 0})
	return string(ub), string(pb), true
}
