package remote

import (
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

const (
	// responseHeaderTimeout 从请求发完到收到响应头的时限；上传完成后 NAS 落盘改名可能较慢，故比连接超时宽松。
	responseHeaderTimeout = 2 * time.Minute
	// metaTimeout 是非上传请求（MKCOL、MOVE、PROPFIND、DELETE）的整体时限。
	metaTimeout = 2 * time.Minute
	// propfindLimit 限制目录列表响应体大小。
	propfindLimit = 32 << 20
)

// WebDAVConfig 是 WebDAV 目标的配置。
type WebDAVConfig struct {
	// URL 是服务地址，只接受 https 或局域网 http，不得内嵌凭据、不得带查询参数。
	URL string
	// Dir 是服务地址下存放备份的目录，用 / 分隔，可多级，不得含 ..。
	Dir      string
	Username string
	Password string
}

// WebDAV 是基于标准库 net/http 的 WebDAV 目标。
type WebDAV struct {
	client   *http.Client
	cfg      WebDAVConfig
	dirURL   *url.URL // 备份目录，路径以 / 结尾
	segments []string // 从服务根到备份目录的各级路径（已解码）
	root     *url.URL
}

var _ Target = (*WebDAV)(nil)

// NewWebDAV 校验配置并构造目标。地址沿用 schema.CheckURL 的字面判定：公网主机只能用 https，
// 局域网主机允许 http。https 使用系统根证书，不支持自签名证书。
// 请求不经环境代理：NAS 在局域网内，且凭据不应绕经意外的中转。
func NewWebDAV(cfg WebDAVConfig) (*WebDAV, error) {
	if _, err := url.Parse(cfg.URL); err != nil {
		return nil, errors.New("WebDAV 地址格式无效")
	}
	if err := schema.CheckURL(cfg.URL, false, false); err != nil {
		return nil, fmt.Errorf("WebDAV 地址无效：%s", scrub(err.Error()))
	}
	root, _ := url.Parse(cfg.URL)
	segs := splitSegments(root.Path)
	for _, s := range splitSegments(cfg.Dir) {
		if s == ".." || s == "." {
			return nil, errors.New("WebDAV 目录不能含有点号路径段")
		}
		segs = append(segs, s)
	}
	for _, s := range segs {
		if s == ".." || s == "." {
			return nil, errors.New("WebDAV 地址路径不能含有点号路径段")
		}
	}
	w := &WebDAV{cfg: cfg, root: root, segments: segs}
	w.dirURL = w.urlFor(segs, true)
	w.client = &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: ConnectTimeout}).DialContext,
			TLSHandshakeTimeout:   ConnectTimeout,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			ResponseHeaderTimeout: responseHeaderTimeout,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return w, nil
}

func splitSegments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// urlFor 由已解码的路径段构造请求地址；String() 会负责转义。
func (w *WebDAV) urlFor(segs []string, dir bool) *url.URL {
	u := &url.URL{Scheme: w.root.Scheme, Host: w.root.Host}
	u.Path = "/" + strings.Join(segs, "/")
	if dir && len(segs) > 0 {
		u.Path += "/"
	}
	return u
}

func (w *WebDAV) fileURL(name string) (*url.URL, error) {
	if name == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return nil, fmt.Errorf("非法文件名 %q", name)
	}
	return w.urlFor(append(append([]string{}, w.segments...), name), false), nil
}

// do 发出请求并把传输层错误归类；返回的响应体由调用方关闭。
func (w *WebDAV) do(ctx context.Context, method string, u *url.URL, body io.Reader, hdr map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, NewError(ReasonIO, "构造请求失败", nil)
	}
	req.SetBasicAuth(w.cfg.Username, w.cfg.Password)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, w.transportError(ctx, err)
	}
	return resp, nil
}

// transportError 把 net/http 的错误转为分类错误：调用方取消原样返回，其余归 connect 或 io。
// *url.Error 带完整地址，所以只保留其内层错误，并抹去口令。
func (w *WebDAV) transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	inner := err
	var ue *url.Error
	if errors.As(err, &ue) {
		inner = ue.Err
	}
	msg := w.redact(inner.Error())
	var ne net.Error
	switch {
	case isCertError(inner):
		return NewError(ReasonConnect, "TLS 证书校验失败（只支持系统信任的证书，局域网可改用 http）："+msg, inner)
	case errors.As(inner, &ne), errors.Is(inner, io.EOF), errors.Is(inner, io.ErrUnexpectedEOF):
		return NewError(ReasonConnect, msg, inner)
	default:
		var op *net.OpError
		if errors.As(inner, &op) {
			return NewError(ReasonConnect, msg, inner)
		}
		return NewError(ReasonIO, msg, inner)
	}
}

func isCertError(err error) bool {
	var cv *tls.CertificateVerificationError
	return errors.As(err, &cv)
}

// redact 去掉文本里的用户名与口令。
func (w *WebDAV) redact(s string) string {
	for _, secret := range []string{w.cfg.Password, w.cfg.Username} {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "***")
		}
	}
	return scrub(s)
}

// statusError 把非预期的 HTTP 状态映射为分类错误。
func (w *WebDAV) statusError(op string, resp *http.Response) *Error {
	status := resp.StatusCode
	msg := fmt.Sprintf("%s：服务器返回 %d %s", op, status, http.StatusText(status))
	switch {
	case status == http.StatusUnauthorized:
		return NewError(ReasonAuth, msg+"，用户名或密码被拒绝", nil)
	case status == http.StatusForbidden:
		return NewError(ReasonNotWritable, msg+"，没有权限", nil)
	case status == http.StatusMethodNotAllowed && (op == "上传" || op == "改名" || op == "删除"):
		return NewError(ReasonNotWritable, msg+"，不允许该操作", nil)
	case status >= 300 && status < 400:
		return NewError(ReasonIO, msg+"，不支持重定向，请检查地址", nil)
	default:
		return NewError(ReasonIO, msg, nil)
	}
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
}

// metaCtx 给非上传请求加整体时限。
func metaCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, metaTimeout)
}

// EnsureDir 逐级发 MKCOL 创建备份目录；201 与 405（已存在）都算成功。
func (w *WebDAV) EnsureDir(ctx context.Context) error {
	ctx, cancel := metaCtx(ctx)
	defer cancel()
	// 地址路径本身那几级也一并确保，但只有目录已存在时才会是 405。
	for i := 1; i <= len(w.segments); i++ {
		u := w.urlFor(w.segments[:i], true)
		resp, err := w.do(ctx, "MKCOL", u, nil, nil)
		if err != nil {
			return err
		}
		status := resp.StatusCode
		drain(resp)
		switch status {
		case http.StatusCreated, http.StatusMethodNotAllowed, http.StatusOK:
		case http.StatusUnauthorized:
			return NewError(ReasonAuth, "创建目录：用户名或密码被拒绝", nil)
		case http.StatusForbidden, http.StatusConflict:
			// 上级是已有路径但不允许创建：留给后续写入判断，这里只在确无该目录时报错。
			if ok, perr := w.dirExists(ctx, u); perr != nil {
				return perr
			} else if !ok {
				return NewError(ReasonNotWritable, fmt.Sprintf("无法创建目录：服务器返回 %d %s", status, http.StatusText(status)), nil)
			}
		default:
			if ok, perr := w.dirExists(ctx, u); perr != nil {
				return perr
			} else if !ok {
				return NewError(ReasonIO, fmt.Sprintf("创建目录：服务器返回 %d %s", status, http.StatusText(status)), nil)
			}
		}
	}
	return nil
}

func (w *WebDAV) dirExists(ctx context.Context, u *url.URL) (bool, error) {
	resp, err := w.do(ctx, "PROPFIND", u, nil, map[string]string{"Depth": "0"})
	if err != nil {
		return false, err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusMultiStatus {
		return true, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return false, w.statusError("检查目录", resp)
	}
	return false, nil
}

// Put 以 chunked 方式 PUT，请求体长度未知；不设总时限，由上层的无进展看门狗负责。
func (w *WebDAV) Put(ctx context.Context, name string, r io.Reader) error {
	u, err := w.fileURL(name)
	if err != nil {
		return NewError(ReasonIO, err.Error(), nil)
	}
	resp, err := w.do(ctx, http.MethodPut, u, io.NopCloser(r), map[string]string{"Content-Type": "application/octet-stream"})
	if err != nil {
		return err
	}
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	}
	return w.statusError("上传", resp)
}

// Rename 用 MOVE 改名，带 Overwrite: T，目标已存在时覆盖。
func (w *WebDAV) Rename(ctx context.Context, from, to string) error {
	src, err := w.fileURL(from)
	if err != nil {
		return NewError(ReasonIO, err.Error(), nil)
	}
	dst, err := w.fileURL(to)
	if err != nil {
		return NewError(ReasonIO, err.Error(), nil)
	}
	ctx, cancel := metaCtx(ctx)
	defer cancel()
	resp, err := w.do(ctx, "MOVE", src, nil, map[string]string{"Destination": dst.String(), "Overwrite": "T"})
	if err != nil {
		return err
	}
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusNoContent, http.StatusOK:
		return nil
	}
	return w.statusError("改名", resp)
}

// Remove 用 DELETE 删除文件；文件不存在视为成功。
func (w *WebDAV) Remove(ctx context.Context, name string) error {
	u, err := w.fileURL(name)
	if err != nil {
		return NewError(ReasonIO, err.Error(), nil)
	}
	ctx, cancel := metaCtx(ctx)
	defer cancel()
	resp, err := w.do(ctx, http.MethodDelete, u, nil, nil)
	if err != nil {
		return err
	}
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	}
	return w.statusError("删除", resp)
}

// Close 关闭空闲连接。
func (w *WebDAV) Close() error {
	w.client.CloseIdleConnections()
	return nil
}

const propfindBody = `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:"><D:prop><D:resourcetype/><D:getcontentlength/></D:prop></D:propfind>`

type multistatus struct {
	Responses []struct {
		Href      string `xml:"href"`
		Propstats []struct {
			Status string `xml:"status"`
			Prop   struct {
				ResourceType struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
				Length string `xml:"getcontentlength"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

// List 用 PROPFIND Depth: 1 列出备份目录的直接子项。
func (w *WebDAV) List(ctx context.Context) ([]Entry, error) {
	ctx, cancel := metaCtx(ctx)
	defer cancel()
	resp, err := w.do(ctx, "PROPFIND", w.dirURL, strings.NewReader(propfindBody),
		map[string]string{"Depth": "1", "Content-Type": "application/xml; charset=utf-8"})
	if err != nil {
		return nil, err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusMultiStatus {
		if resp.StatusCode == http.StatusNotFound {
			return nil, NewError(ReasonIO, "列目录：备份目录不存在", nil)
		}
		return nil, w.statusError("列目录", resp)
	}
	var ms multistatus
	if err := xml.NewDecoder(io.LimitReader(resp.Body, propfindLimit)).Decode(&ms); err != nil {
		return nil, NewError(ReasonIO, "列目录：无法解析服务器响应", err)
	}
	self := strings.Join(w.segments, "/")
	var out []Entry
	for _, r := range ms.Responses {
		p := r.Href
		if u, err := url.Parse(p); err == nil {
			p = u.Path // 已解码；兼容绝对地址与相对路径
		}
		p = strings.Trim(p, "/")
		if p == self || p == "" {
			continue
		}
		base := path.Base(p)
		if strings.TrimSuffix(strings.TrimSuffix(p, base), "/") != self {
			continue // 不是备份目录的直接子项
		}
		e := Entry{Name: base}
		for _, ps := range r.Propstats {
			if ps.Prop.ResourceType.Collection != nil {
				e.IsDir = true
			}
			if n, err := strconv.ParseInt(strings.TrimSpace(ps.Prop.Length), 10, 64); err == nil {
				e.Size = n
			}
		}
		out = append(out, e)
	}
	return out, nil
}
