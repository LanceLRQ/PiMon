package install

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
)

type (
	execRunner = sysinstall.ExecRunner
	osFS       = sysinstall.OSFS
	osUsers    = sysinstall.OSUsers
)

// buildEnv 返回子进程环境：去掉 base 里全部 PIMON_* 再追加 set。
func buildEnv(base, set []string) []string { return sysinstall.BuildEnv(base, set) }

// httpHealth 探测服务是否就绪：先明文 GET，失败（连接错误或非 2xx，含 TLS 端口对明文请求回的 400）
// 后，对同一端口再用 https 探一次（自签名证书，只访问回环、只看状态码）；任一次 2xx 即就绪。
func httpHealth(ctx context.Context, url string) error {
	plainErr := probeHealth(ctx, http.DefaultClient, url)
	if plainErr == nil {
		return nil
	}
	rest, ok := strings.CutPrefix(url, "http://")
	if !ok {
		return plainErr
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // 只探测回环地址，仅看状态码
	}}
	defer client.CloseIdleConnections()
	if err := probeHealth(ctx, client, "https://"+rest); err != nil {
		return fmt.Errorf("http: %v；https: %w", plainErr, err)
	}
	return nil
}

// probeHealth 对地址做一次 GET，2xx 视为就绪。
func probeHealth(ctx context.Context, client *http.Client, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
