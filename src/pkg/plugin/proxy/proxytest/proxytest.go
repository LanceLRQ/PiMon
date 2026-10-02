// Package proxytest 提供测试用的假代理服务器：最小 HTTP 代理、最小 SOCKS5 服务器，
// 以及真实转发的 CONNECT / SOCKS5 变体。
//
// NewHTTP、NewSocks5 不真正转发流量，而是自己扮演目标站点，对收到的 HTTP 请求回 204，
// 并记录代理侧看到的目标地址与认证信息，供断言"请求确实经过代理"。
// NewSocks5Forward、NewHTTPConnect、NewHTTPSConnect 会真实拨目标并双向转发原始 TCP，
// 用于验证隧道上的任意协议。仅用于测试，只应拨本机地址。
package proxytest

import (
	"bufio"
	"crypto/x509"
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
	// CertPool 仅 HTTPS 代理有值：信任该代理证书的根池。
	CertPool *x509.CertPool

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
	return newSocks5(t, user, pass, false)
}

// NewSocks5Forward 与 NewSocks5 相同，但握手后真实拨目标并双向转发，不再扮演目标站点。
func NewSocks5Forward(t testing.TB, user, pass string) *Server {
	t.Helper()
	return newSocks5(t, user, pass, true)
}

func newSocks5(t testing.TB, user, pass string, forward bool) *Server {
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
			go s.serve(c, user, pass, forward)
		}
	}()
	return s
}

func (s *Server) serve(c net.Conn, user, pass string, forward bool) {
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
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb))))
	s.record(Seen{Target: target, Domain: domain, User: gotUser, Password: gotPass})
	if forward {
		up, err := net.Dial("tcp", target)
		if err != nil {
			_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
			return
		}
		defer func() { _ = up.Close() }()
		if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
			return
		}
		pipe(c, br, up)
		return
	}
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

// NewHTTPConnect 启动真实转发的假 HTTP 代理，只处理 CONNECT。
// user 非空时要求 Basic 代理认证，缺失或错误回 407。
func NewHTTPConnect(t testing.TB, user, pass string) *Server {
	t.Helper()
	s := &Server{}
	srv := httptest.NewServer(s.connectHandler(user, pass))
	t.Cleanup(srv.Close)
	s.Addr = srv.Listener.Addr().String()
	return s
}

// NewHTTPSConnect 与 NewHTTPConnect 相同，但代理本身走 TLS（httptest 自签证书，
// 证书覆盖 127.0.0.1），Server.CertPool 为信任该证书的根池。
func NewHTTPSConnect(t testing.TB, user, pass string) *Server {
	t.Helper()
	s := &Server{}
	srv := httptest.NewUnstartedServer(s.connectHandler(user, pass))
	srv.StartTLS()
	t.Cleanup(srv.Close)
	s.Addr = srv.Listener.Addr().String()
	s.CertPool = x509.NewCertPool()
	s.CertPool.AddCert(srv.Certificate())
	return s
}

func (s *Server) connectHandler(user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "仅支持 CONNECT", http.StatusMethodNotAllowed)
			return
		}
		gotUser, gotPass := "", ""
		if ph := r.Header.Get("Proxy-Authorization"); ph != "" {
			req := &http.Request{Header: http.Header{"Authorization": {ph}}}
			gotUser, gotPass, _ = req.BasicAuth()
		}
		s.record(Seen{Target: r.Host, User: gotUser, Password: gotPass})
		if user != "" && (gotUser != user || gotPass != pass) {
			w.Header().Set("Proxy-Authenticate", `Basic realm="proxytest"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = up.Close() }()
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		c, brw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		pipe(c, brw.Reader, up)
	})
}

// pipe 在客户端连接与上游之间双向拷贝，任一方向结束即返回。
func pipe(c net.Conn, cr io.Reader, up net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, cr); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
	<-done
}
