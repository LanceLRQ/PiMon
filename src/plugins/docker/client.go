package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// clientVersionCeiling 是本客户端声明支持的 Engine API 上限；
// 实际请求用 min(服务端版本, 上限)，避免旧版本写死被新 daemon 拒绝。
const clientVersionCeiling = "1.52"

// maxBody 限制单次响应体大小。
const maxBody = 8 << 20

type listEntry struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
	State string   `json:"State"`
}

type inspectResult struct {
	RestartCount int `json:"RestartCount"`
	State        struct {
		Status     string `json:"Status"`
		Restarting bool   `json:"Restarting"`
		OOMKilled  bool   `json:"OOMKilled"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Healthcheck *struct {
			StartPeriod int64 `json:"StartPeriod"`
		} `json:"Healthcheck"`
	} `json:"Config"`
}

type client struct {
	socket string
	http   *http.Client
	tr     *http.Transport
	prefix string // 形如 "/v1.52"，拿不到版本时为空
}

func newClient(socket string) *client {
	tr := &http.Transport{
		// 显式不走代理：ProxyFromEnvironment 会改写发往 unix socket 的请求。
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		DisableKeepAlives: true,
	}
	return &client{socket: socket, tr: tr, http: &http.Client{Transport: tr, Timeout: 30 * time.Second}}
}

func (c *client) close() { c.tr.CloseIdleConnections() }

// get 请求路径（不含版本前缀）并读取有上限的响应体。
func (c *client) get(ctx context.Context, p string, versioned bool) ([]byte, http.Header, error) {
	u := "http://docker"
	if versioned {
		u += c.prefix
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+p, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, nil, err
	}
	if len(body) > maxBody {
		return nil, nil, errors.New("响应过大 / Docker response too large")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("docker 返回 HTTP %d / Docker returned HTTP %d", resp.StatusCode, resp.StatusCode)
	}
	return body, resp.Header, nil
}

// negotiate 请求 /_ping，按 API-Version 响应头确定后续请求的版本前缀。
func (c *client) negotiate(ctx context.Context) error {
	_, hdr, err := c.get(ctx, "/_ping", false)
	if err != nil {
		return c.explain(err)
	}
	c.prefix = ""
	if v := parseVersion(hdr.Get("API-Version")); v != nil {
		if ceil := parseVersion(clientVersionCeiling); lessVersion(ceil, v) {
			v = ceil
		}
		c.prefix = fmt.Sprintf("/v%d.%d", v[0], v[1])
	}
	return nil
}

func parseVersion(s string) []int {
	a, b, ok := strings.Cut(strings.TrimSpace(s), ".")
	if !ok {
		return nil
	}
	major, e1 := strconv.Atoi(a)
	minor, e2 := strconv.Atoi(b)
	if e1 != nil || e2 != nil || major < 1 {
		return nil
	}
	return []int{major, minor}
}

func lessVersion(a, b []int) bool {
	if a[0] != b[0] {
		return a[0] < b[0]
	}
	return a[1] < b[1]
}

func (c *client) list(ctx context.Context, all bool) ([]listEntry, error) {
	q := url.Values{"all": {"0"}}
	if all {
		q.Set("all", "1")
	}
	body, _, err := c.get(ctx, "/containers/json?"+q.Encode(), true)
	if err != nil {
		return nil, c.explain(err)
	}
	var out []listEntry
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("无法解析容器列表 / Cannot parse container list: %w", err)
	}
	return out, nil
}

func (c *client) inspect(ctx context.Context, id string) (inspectResult, error) {
	var out inspectResult
	body, _, err := c.get(ctx, "/containers/"+url.PathEscape(id)+"/json", true)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(body, &out)
	return out, err
}

// explain 把连接类错误翻译成可操作的中文说明；其他错误原样返回。
func (c *client) explain(err error) error {
	const hint = "需要把 agent 运行用户加入 docker 组（近似 root 权限）/ The agent user must be in the docker group (close to root access)"
	switch {
	case errors.Is(err, os.ErrPermission), errors.Is(err, syscall.EACCES):
		return fmt.Errorf("没有权限访问 Docker socket %s：%s", c.socket, hint)
	case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOENT):
		return fmt.Errorf("找不到 Docker socket %s（Docker 未安装或未运行）。若已安装：%s", c.socket, hint)
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("docker socket %s 拒绝连接（Docker 未运行？）。%s", c.socket, hint)
	}
	return fmt.Errorf("访问 Docker 失败 / Docker request failed: %w", err)
}
