// Package httpjson 是内置的 HTTP JSON 插件：GET 一个地址，响应体必须是报告格式。
// 它没有 outputs 与 widgets 声明，数据项由响应自动识别，只能用通用小组件显示。
package httpjson

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

//go:embed plugin.yaml
var manifestYAML []byte

// maxBody 是响应体大小上限（字节）。
const maxBody = 1 << 20

type plugin struct{ m *manifest.Manifest }

func init() {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic("http-json 内置 manifest 不合法: " + err.Error())
	}
	runtime.Register(&plugin{m: m})
}

func (p *plugin) Manifest() *manifest.Manifest { return p.m }

func (p *plugin) Collect(ctx context.Context, in runtime.Input) (*report.Report, error) {
	raw, _ := in.Config["url"].(string)
	if raw == "" {
		return nil, errors.New("未配置 url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errors.New("url 不合法")
	}
	host := req.URL.Host
	req.Header.Set("Accept", "application/json")
	for k, v := range headers(in) {
		req.Header.Set(k, v)
	}

	cli := &http.Client{
		Transport: in.Proxy.Transport(),
		// 自定义密钥请求头不会被 Go 在跨主机重定向时剥除，所以跨主机一律拒绝，同主机才跟随。
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("重定向次数过多")
			}
			if next.URL.Scheme != req.URL.Scheme || next.URL.Host != req.URL.Host {
				return errors.New("拒绝跨主机重定向")
			}
			return nil
		},
	}
	resp, err := cli.Do(req)
	if err != nil {
		// url.Error 带完整地址（查询参数里常有令牌），只保留底层原因与主机名。
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("请求 %s 失败: %w", host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s 返回状态 %d", host, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("读取 %s 响应失败: %w", host, err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("响应体超过 %d 字节上限", maxBody)
	}
	return report.Parse(body, nil)
}

// headers 合并普通请求头与密钥值：密钥在 Secrets 里以 headers.<名称> 为键。
func headers(in runtime.Input) map[string]string {
	cfg, _ := in.Config["headers"].(map[string]any)
	names := make([]string, 0, len(cfg))
	for k := range cfg {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make(map[string]string, len(names))
	for _, k := range names {
		if s, ok := in.Secrets["headers."+k]; ok {
			out[k] = s
		} else if s, ok := cfg[k].(string); ok {
			out[k] = s
		}
	}
	return out
}
