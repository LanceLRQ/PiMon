// Package probe 是 http-check、tcp-check、net-reach 共用的网络探测函数。
//
// 探测的“不通”（连接失败、超时、状态码不符）属于正常的检查结果，由调用方决定如何呈现；
// 本包返回的错误不含完整地址（查询参数里常有令牌），只保留底层原因与主机名。
package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
)

// ErrTimeout 表示探测超过了自身的超时时间（不是上层 ctx 到期）。
var ErrTimeout = errors.New("超时 / Timeout")

// maxBody 是关键字检查最多读取的响应体字节数。
const maxBody = 1 << 20

// maxRedirects 是跟随重定向的次数上限。
const maxRedirects = 10

// HTTPOptions 是一次 HTTP 探测的参数。
type HTTPOptions struct {
	URL    string
	Method string // GET 或 HEAD，空为 GET
	Proxy  *proxy.Proxy
	// Timeout 为 0 表示只受 ctx 约束。
	Timeout         time.Duration
	FollowRedirects bool
	SkipTLSVerify   bool
	// Keyword 非空时读取响应体并检查是否包含。
	Keyword string
	Clock   clock.Clock
}

// HTTPResult 是一次 HTTP 探测的结果；只有请求拿到响应时才有值。
type HTTPResult struct {
	Code    int
	Latency time.Duration
	// CertNotAfter 是最终响应的叶子证书到期时间，非 https 时为零值。
	CertNotAfter time.Time
	// KeywordFound 仅在 Keyword 非空时有意义。
	KeywordFound bool
}

// HTTP 发起一次 HTTP 请求。拿不到响应时返回错误：上层 ctx 结束返回 ctx.Err()，
// 超过 Timeout 返回 ErrTimeout，其余为剥离了地址的底层原因。
func HTTP(ctx context.Context, o HTTPOptions) (HTTPResult, error) {
	clk := o.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	method := o.Method
	if method == "" {
		method = http.MethodGet
	}
	tctx, cancel := ctx, context.CancelFunc(func() {})
	if o.Timeout > 0 {
		tctx, cancel = context.WithTimeout(ctx, o.Timeout)
	}
	defer cancel()

	req, err := http.NewRequestWithContext(tctx, method, o.URL, nil)
	if err != nil {
		return HTTPResult{}, errors.New("url 不合法")
	}
	host := req.URL.Host

	tr := o.Proxy.Transport()
	if o.SkipTLSVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 由用户显式开启，面向自签名的内网服务
	}
	cli := &http.Client{
		Transport: tr,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if !o.FollowRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) >= maxRedirects {
				return errors.New("重定向次数过多")
			}
			return nil
		},
	}
	defer tr.CloseIdleConnections()

	start := clk.Now()
	resp, err := cli.Do(req)
	if err != nil {
		return HTTPResult{}, classify(ctx, tctx, host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	res := HTTPResult{Code: resp.StatusCode, Latency: clk.Now().Sub(start)}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		res.CertNotAfter = resp.TLS.PeerCertificates[0].NotAfter
	}
	if o.Keyword != "" {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		if err != nil && len(body) == 0 {
			return res, classify(ctx, tctx, host, err)
		}
		res.KeywordFound = strings.Contains(string(body), o.Keyword)
	}
	return res, nil
}

// TCP 向 addr（host:port）发起一次 TCP 连接并立即关闭，返回建立连接的耗时。
// 代理只接受 socks；选了 http 代理时错误满足 errors.Is(err, proxy.ErrRawTCPUnsupported)。
func TCP(ctx context.Context, addr string, pr *proxy.Proxy, timeout time.Duration, clk clock.Clock) (time.Duration, error) {
	if clk == nil {
		clk = clock.Real{}
	}
	tctx, cancel := ctx, context.CancelFunc(func() {})
	if timeout > 0 {
		tctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	start := clk.Now()
	conn, err := pr.DialContext()(tctx, "tcp", addr)
	if err != nil {
		return 0, classify(ctx, tctx, addr, err)
	}
	_ = conn.Close()
	return clk.Now().Sub(start), nil
}

// JoinHostPort 组装 host:port。
func JoinHostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// classify 把底层错误归类：上层 ctx 结束、自身超时、其余（去掉 url.Error 里的完整地址）。
func classify(parent, own context.Context, host string, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	// 连接层的读写截止时间取自 ctx 的截止时间，可能比 ctx.Err() 翻转得更早，所以也认网络超时。
	var ne net.Error
	if errors.Is(own.Err(), context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return ErrTimeout
	}
	if errors.Is(err, proxy.ErrRawTCPUnsupported) {
		return err
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("请求 %s 失败: %w", host, err)
}
