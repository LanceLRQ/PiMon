// Package server 负责 HTTP(S) 服务的运行与优雅退出。
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

const readHeaderTimeout = 10 * time.Second

// idleTimeout 是保持连接的最长空闲时间；不设 ReadTimeout，以免影响 WebSocket 与下载。
const idleTimeout = 120 * time.Second

// shutdownTimeout 是优雅关闭的最长等待时间，测试中可改小。
var shutdownTimeout = 10 * time.Second

// Listen 监听 TCP 地址，失败时给出端口占用的提示。
func Listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("监听 %s 失败（端口可能被占用）: %w", addr, err)
	}
	return ln, nil
}

// Run 在 ln 上提供服务：tlsCert 为 nil 时是 HTTP，否则是 HTTPS（最低 TLS 1.2）。
// 开始 Serve 后调用 onReady（可为 nil）；ctx 结束后最多等 10 秒优雅关闭。
// 正常关闭返回 nil。
func Run(ctx context.Context, ln net.Listener, h http.Handler, tlsCert *tls.Certificate, onReady func()) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: readHeaderTimeout, IdleTimeout: idleTimeout}
	if tlsCert != nil {
		ln = tls.NewListener(ln, &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{*tlsCert},
			NextProtos:   []string{"h2", "http/1.1"},
		})
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	if onReady != nil {
		onReady()
	}
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := srv.Shutdown(shutCtx)
	if err != nil {
		// 优雅关闭超时，强制断开残留连接（如长连接）。
		_ = srv.Close()
	}
	<-serveErr
	return err
}
