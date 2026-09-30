package server

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/tlscert"
)

var hello = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "hi") })

func start(t *testing.T, cert *tls.Certificate) (addr string, cancel context.CancelFunc, done chan error) {
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done = make(chan error, 1)
	go func() { done <- Run(ctx, ln, hello, cert, func() { close(ready) }) }()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("onReady 未被回调")
	}
	return ln.Addr().String(), cancel, done
}

func stop(t *testing.T, cancel context.CancelFunc, done chan error) {
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("正常关闭应返回 nil: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run 未返回")
	}
}

func TestServeHTTP(t *testing.T) {
	addr, cancel, done := start(t, nil)
	resp, err := http.Get("http://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "hi" {
		t.Fatalf("响应 %q", b)
	}
	stop(t, cancel, done)
}

func TestServeTLS(t *testing.T) {
	dir := t.TempDir()
	cert, err := tlscert.LoadOrCreate(dir+"/c.crt", dir+"/c.key", time.Now(), tlscert.Env{})
	if err != nil {
		t.Fatal(err)
	}
	addr, cancel, done := start(t, &cert)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // 测试自签名证书
	resp, err := client.Get("https://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.TLS == nil || resp.TLS.Version < tls.VersionTLS12 {
		t.Fatal("应使用 TLS 1.2 及以上")
	}
	stop(t, cancel, done)
}

func TestListenInUseHint(t *testing.T) {
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, err = Listen(ln.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "端口可能被占用") {
		t.Fatalf("错误应提示端口占用: %v", err)
	}
}

func TestServeTLSNegotiatesHTTP2(t *testing.T) {
	dir := t.TempDir()
	cert, err := tlscert.LoadOrCreate(dir+"/c.crt", dir+"/c.key", time.Now(), tlscert.Env{})
	if err != nil {
		t.Fatal(err)
	}
	addr, cancel, done := start(t, &cert)
	client := &http.Client{Transport: &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // 测试自签名证书
	}}
	resp, err := client.Get("https://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("期望协商到 HTTP/2，实际 %s", resp.Proto)
	}
	stop(t, cancel, done)
}

func TestShutdownTimeoutForcesClose(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 100 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = old })

	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{})
	blocking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, ln, blocking, nil, nil) }()

	reqErr := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err == nil {
			_ = resp.Body.Close()
		}
		reqErr <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("关闭超时应返回错误")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("关闭超时后 Run 未返回")
	}
	select {
	case err := <-reqErr:
		if err == nil {
			t.Fatal("残留连接应被强制断开")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("残留连接未被断开")
	}
}
