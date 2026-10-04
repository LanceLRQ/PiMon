package upgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
)

const (
	// downloadTimeout 是单个文件下载的总时限（网络操作，用真实时间）。
	downloadTimeout = 10 * time.Minute
	dialTimeout     = 10 * time.Second
	headerTimeout   = 30 * time.Second
	// maxSumsSize 是 SHA256SUMS 的大小上限。
	maxSumsSize = 64 << 10
)

// statusError 表示服务器返回了非 2xx 状态码。
type statusError struct{ Code int }

func (e *statusError) Error() string { return "状态码 " + strconv.Itoa(e.Code) }

// requestError 表示请求未拿到响应（连接、TLS 握手失败等），区别于读响应体时出错。
type requestError struct{ err error }

func (e *requestError) Error() string { return e.err.Error() }
func (e *requestError) Unwrap() error { return e.err }

// get 发 GET 并检查状态码；连接失败返回 *requestError，非 2xx 返回 *statusError。成功时调用方负责关闭 Body。
func get(ctx context.Context, c *http.Client, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, &requestError{err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_ = resp.Body.Close()
		return nil, &statusError{Code: resp.StatusCode}
	}
	return resp, nil
}

// fetchToTemp 下载 rawURL 到 dir 下的临时文件，边写边算 SHA-256；超过 maxSize 或下载中断时删除临时文件并返回错误。
func fetchToTemp(ctx context.Context, c *http.Client, rawURL, dir string, maxSize int64) (*tempFile, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	resp, err := get(ctx, c, rawURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.ContentLength > maxSize {
		return nil, fmt.Errorf("文件大小 %d 字节超过上限 %d 字节", resp.ContentLength, maxSize)
	}
	f, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return nil, fmt.Errorf("创建临时文件: %w", err)
	}
	t := &tempFile{f: f}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxSize+1))
	switch {
	case err != nil:
		t.discard()
		return nil, fmt.Errorf("下载中断: %w", err)
	case n > maxSize:
		t.discard()
		return nil, fmt.Errorf("文件超过大小上限 %d 字节", maxSize)
	}
	t.Sum = hex.EncodeToString(h.Sum(nil))
	return t, nil
}

// fetchSmall 下载不超过 maxSize 字节的小文件到内存。
func fetchSmall(ctx context.Context, c *http.Client, rawURL string, maxSize int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	resp, err := get(ctx, c, rawURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(resp.Body, maxSize+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("下载中断: %w", err)
	case n > maxSize:
		return nil, fmt.Errorf("文件超过大小上限 %d 字节", maxSize)
	}
	return buf.Bytes(), nil
}

// hubTransport 返回下载 hub 用的传输：直连（不读 HTTP_PROXY 等环境变量）；pin 非空时 TLS 只认钉扎的 SPKI。
func hubTransport(pin *certs.SPKI) *http.Transport {
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: headerTimeout,
		DisableKeepAlives:     true,
	}
	if pin != nil {
		// 标准校验让位给钉扎：VerifyConnection 在 InsecureSkipVerify 下仍会执行，只比对叶子证书 SPKI。
		tr.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, //nolint:gosec // 由 VerifyConnection 做 SPKI 钉扎校验
			VerifyConnection:   certs.PinVerifier(*pin),
		}
	}
	return tr
}

// hubClient 不跟随重定向：hub 的下载路由不会重定向，3xx 按非 2xx 处理。
func hubClient(pin *certs.SPKI) *http.Client {
	return &http.Client{
		Transport:     hubTransport(pin),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// hubDownloadURL 拼出 <scheme>://<host>:<port>/agent/download/<平台>；host 必须是 IP（来自会话对端地址）。
func hubDownloadURL(scheme, host string, port int, platform string) (string, error) {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return "", fmt.Errorf("hub 地址 %q 不是合法的 IP", clip(host))
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("hub 网页端口 %d 无效", port)
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(addr.String(), strconv.Itoa(port)), Path: "/agent/download/" + platform}
	return u.String(), nil
}

// fetchFromHub 先用 http 下载；连接失败或非 2xx 时改用同端口 https（TLS 钉扎 hub SPKI）重试。
// 两种方式都只是传输，结果都要由调用方用经钉扎通道拿到的校验和校验。
func fetchFromHub(ctx context.Context, d Deps, host string, port int, pin certs.SPKI, platform, dir string) (*tempFile, error) {
	plain, err := hubDownloadURL("http", host, port, platform)
	if err != nil {
		return nil, err
	}
	secure, _ := hubDownloadURL("https", host, port, platform)
	say(d.Out, "从 hub 下载 %s", plain)
	t, err := fetchToTemp(ctx, hubClient(nil), plain, dir, d.maxSize())
	if err == nil {
		return t, nil
	}
	var re *requestError
	var se *statusError
	if !errors.As(err, &re) && !errors.As(err, &se) {
		return nil, err
	}
	say(d.Out, "HTTP 下载失败（%v），改用 HTTPS（钉扎 hub 证书）重试 %s", err, secure)
	t, herr := fetchToTemp(ctx, hubClient(&pin), secure, dir, d.maxSize())
	if herr != nil {
		var pm *certs.PinMismatchError
		if errors.As(herr, &pm) {
			return nil, fmt.Errorf("HTTP 下载失败（%v）；改用 HTTPS 时 hub 证书指纹与本机钉扎不符，拒绝下载（hub 证书重建过请先 repin）: %w", err, pm)
		}
		return nil, fmt.Errorf("HTTP 下载失败（%v）；改用 HTTPS 也失败: %w", err, herr)
	}
	return t, nil
}

// releaseClient 用 --proxy 指定的代理（空为直连）；允许跟随重定向（GitHub 会跳到对象存储），但不允许从 https 降级到 http。
func releaseClient(px *proxy.Proxy) *http.Client {
	return &http.Client{
		Transport: px.Transport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("重定向次数过多")
			}
			if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("拒绝从 HTTPS 重定向到 HTTP")
			}
			return nil
		},
	}
}

// clip 把对端给出的字符串截到 64 字节再进错误文本。
func clip(s string) string {
	if len(s) > 64 {
		return strings.ToValidUTF8(s[:64], "")
	}
	return s
}
