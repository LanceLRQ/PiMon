package upgrade

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
	"github.com/LanceLRQ/PiMon/src/internal/agent/install"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink/agentlinktest"
)

func TestHub_HTTP成功升级并重启用户服务(t *testing.T) {
	f := newFixture(t)
	hits := &counter{}
	srv := httptest.NewServer(hubHandler(hits, "linux-amd64", newBin))
	defer srv.Close()
	f.useHub(srv)

	if err := Run(context.Background(), f.d, Options{}); err != nil {
		t.Fatalf("升级失败: %v\n%s", err, f.out.String())
	}
	f.assertReplaced()
	want := [][]string{{"/usr/bin/systemctl", "--user", "restart", install.ServiceName}}
	if got := f.runner.calls(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("重启命令 %v", got)
	}
	if hits.get("/agent/download/linux-amd64") != 1 {
		t.Fatalf("下载请求 %v", hits.n)
	}
	if !strings.Contains(f.out.String(), "v0.2.0") {
		t.Fatalf("输出缺少新版本: %s", f.out.String())
	}
}

func TestHub_重启命令按模式(t *testing.T) {
	cases := []struct {
		name   string
		goos   string
		euid   int
		system bool
		want   []string
	}{
		{"Linux 系统模式", "linux", 0, true, []string{"/usr/bin/systemctl", "restart", install.ServiceName}},
		{"Linux 用户模式", "linux", 1000, false, []string{"/usr/bin/systemctl", "--user", "restart", install.ServiceName}},
		{"macOS", "darwin", 501, false, []string{"/bin/launchctl", "kill", "SIGTERM", "gui/501/" + install.LaunchdLabel}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			srv := httptest.NewServer(hubHandler(&counter{}, tc.goos+"-arm64", newBin))
			defer srv.Close()
			f.useHub(srv)
			f.d.GOOS, f.d.GOARCH, f.d.EUID, f.d.Paths.System = tc.goos, "arm64", tc.euid, tc.system
			if err := Run(context.Background(), f.d, Options{}); err != nil {
				t.Fatalf("升级失败: %v", err)
			}
			f.assertReplaced()
			if got := f.runner.calls(); len(got) != 1 || !slices.Equal(got[0], tc.want) {
				t.Fatalf("重启命令 %v，期望 %v", got, tc.want)
			}
		})
	}
}

func TestHub_校验和不匹配不替换(t *testing.T) {
	f := newFixture(t)
	srv := httptest.NewServer(hubHandler(&counter{}, "linux-amd64", newBin))
	defer srv.Close()
	f.useHub(srv)
	f.info.SHA256 = sum("something-else")

	err := Run(context.Background(), f.d, Options{})
	if err == nil || !strings.Contains(err.Error(), "校验和不匹配") {
		t.Fatalf("err=%v", err)
	}
	f.assertUnchanged()
	if len(f.runner.calls()) != 0 {
		t.Fatal("校验失败不应重启")
	}
}

func TestHub_同版本跳过(t *testing.T) {
	f := newFixture(t)
	hits := &counter{}
	srv := httptest.NewServer(hubHandler(hits, "linux-amd64", newBin))
	defer srv.Close()
	f.useHub(srv)
	f.info.HubVersion = f.d.Version

	if err := Run(context.Background(), f.d, Options{}); err != nil {
		t.Fatal(err)
	}
	f.assertUnchanged()
	if hits.get("/agent/download/linux-amd64") != 0 || len(f.runner.calls()) != 0 {
		t.Fatal("同版本不应下载或重启")
	}
	if !strings.Contains(f.out.String(), "无需升级") {
		t.Fatalf("输出 %q", f.out.String())
	}
}

func TestHub_新旧内容相同跳过(t *testing.T) {
	f := newFixture(t)
	hits := &counter{}
	srv := httptest.NewServer(hubHandler(hits, "linux-amd64", oldBin))
	defer srv.Close()
	f.useHub(srv)
	f.info.SHA256 = sum(oldBin)

	if err := Run(context.Background(), f.d, Options{}); err != nil {
		t.Fatal(err)
	}
	f.assertUnchanged()
	if hits.get("/agent/download/linux-amd64") != 0 || len(f.runner.calls()) != 0 {
		t.Fatal("内容相同不应下载或重启")
	}
	if !strings.Contains(f.out.String(), "无需升级") {
		t.Fatalf("输出 %q", f.out.String())
	}
}

func TestHub_下载中断不替换(t *testing.T) {
	f := newFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(newBin)))
		_, _ = w.Write([]byte(newBin[:5]))
		w.(http.Flusher).Flush()
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer srv.Close()
	f.useHub(srv)

	if err := Run(context.Background(), f.d, Options{}); err == nil {
		t.Fatal("下载中断应失败")
	}
	f.assertUnchanged()
	if len(f.runner.calls()) != 0 {
		t.Fatal("不应重启")
	}
}

func TestHub_超过大小上限不替换(t *testing.T) {
	for _, declared := range []bool{true, false} {
		t.Run("Content-Length="+strconv.FormatBool(declared), func(t *testing.T) {
			f := newFixture(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if declared {
					w.Header().Set("Content-Length", strconv.Itoa(len(newBin)))
				} else {
					_, _ = w.Write([]byte(newBin[:3]))
					w.(http.Flusher).Flush() // 分块传输，没有 Content-Length
				}
				if declared {
					_, _ = w.Write([]byte(newBin))
				} else {
					_, _ = w.Write([]byte(newBin[3:]))
				}
			}))
			defer srv.Close()
			f.useHub(srv)
			f.d.MaxSize = 10

			err := Run(context.Background(), f.d, Options{})
			if err == nil || !strings.Contains(err.Error(), "上限 10 字节") {
				t.Fatalf("err=%v", err)
			}
			f.assertUnchanged()
		})
	}
}

func TestHub_HTTP失败改用钉扎HTTPS(t *testing.T) {
	f := newFixture(t)
	hits := &counter{}
	srv := httptest.NewTLSServer(hubHandler(hits, "linux-amd64", newBin))
	defer srv.Close()
	f.useHub(srv)
	f.pinTo(srv)

	if err := Run(context.Background(), f.d, Options{}); err != nil {
		t.Fatalf("升级失败: %v\n%s", err, f.out.String())
	}
	f.assertReplaced()
	if !strings.Contains(f.out.String(), "HTTPS") {
		t.Fatalf("输出应说明改用 HTTPS: %s", f.out.String())
	}
}

func TestHub_HTTPS指纹不符拒绝(t *testing.T) {
	f := newFixture(t)
	hits := &counter{}
	srv := httptest.NewTLSServer(hubHandler(hits, "linux-amd64", newBin))
	defer srv.Close()
	f.useHub(srv)
	_, other := agentlinktest.NewCert(t)
	f.conf.HubPin = other.String()

	err := Run(context.Background(), f.d, Options{})
	if err == nil || !strings.Contains(err.Error(), "指纹") {
		t.Fatalf("err=%v", err)
	}
	f.assertUnchanged()
	if hits.get("/agent/download/linux-amd64") != 0 {
		t.Fatal("指纹不符时不应发出请求")
	}
}

func TestHub_HTTP非2xx后改HTTPS仍失败(t *testing.T) {
	f := newFixture(t)
	hits := &counter{}
	// 纯 HTTP 服务对本平台返回 404；改用 HTTPS 时握手失败。
	srv := httptest.NewServer(hubHandler(hits, "darwin-arm64", newBin))
	defer srv.Close()
	f.useHub(srv)

	err := Run(context.Background(), f.d, Options{})
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("err=%v", err)
	}
	if hits.get("/agent/download/linux-amd64") != 1 {
		t.Fatalf("应先尝试 HTTP: %v", hits.n)
	}
	f.assertUnchanged()
}

func TestHub_系统模式非root报错(t *testing.T) {
	f := newFixture(t)
	f.d.Paths.System = true
	err := Run(context.Background(), f.d, Options{})
	if err == nil || !strings.Contains(err.Error(), "sudo pimon-agent upgrade") {
		t.Fatalf("err=%v", err)
	}
	if f.infoCalls != 0 {
		t.Fatal("权限不足时不应请求升级信息")
	}
	f.assertUnchanged()
}

func TestHub_用户模式以root执行报错(t *testing.T) {
	f := newFixture(t)
	f.d.EUID = 0
	err := Run(context.Background(), f.d, Options{})
	if err == nil || !strings.Contains(err.Error(), "不要用 sudo") {
		t.Fatalf("err=%v", err)
	}
	if f.infoCalls != 0 || len(f.runner.calls()) != 0 {
		t.Fatal("不应继续")
	}
	f.assertUnchanged()
}

func TestHub_未连接hub(t *testing.T) {
	f := newFixture(t)
	f.infoErr = &ctl.RemoteError{Msg: "未连接 hub"}
	err := Run(context.Background(), f.d, Options{})
	var re *ctl.RemoteError
	if err == nil || !strings.Contains(err.Error(), "未连接 hub") || !errors.As(err, &re) {
		t.Fatalf("err=%v", err)
	}
	f.assertUnchanged()
}

func TestHub_未内嵌本平台(t *testing.T) {
	f := newFixture(t)
	f.info.SHA256 = ""
	f.info.HubHost, f.info.WebPort = "127.0.0.1", 1
	err := Run(context.Background(), f.d, Options{})
	if err == nil || !strings.Contains(err.Error(), "linux-amd64") || !strings.Contains(err.Error(), "--release") {
		t.Fatalf("err=%v", err)
	}
	f.assertUnchanged()
}

func TestHub_非法主机与端口拒绝(t *testing.T) {
	for _, tc := range []struct {
		host string
		port int
	}{{"127.0.0.1/evil", 80}, {"evil.example@127.0.0.1", 80}, {"", 80}, {"127.0.0.1", 0}, {"127.0.0.1", 70000}} {
		f := newFixture(t)
		f.info.HubHost, f.info.WebPort = tc.host, tc.port
		if err := Run(context.Background(), f.d, Options{}); err == nil {
			t.Fatalf("%q:%d 应被拒绝", tc.host, tc.port)
		}
		f.assertUnchanged()
	}
}

func TestHub_重启失败提示已替换(t *testing.T) {
	f := newFixture(t)
	srv := httptest.NewServer(hubHandler(&counter{}, "linux-amd64", newBin))
	defer srv.Close()
	f.useHub(srv)
	f.runner.err = errors.New("unit not found")

	err := Run(context.Background(), f.d, Options{})
	if err == nil || !strings.Contains(err.Error(), "已替换") {
		t.Fatalf("err=%v", err)
	}
	f.assertReplaced()
}

func TestHub_目标二进制不存在(t *testing.T) {
	f := newFixture(t)
	f.d.Paths.Bin = f.bin + "-missing"
	if err := Run(context.Background(), f.d, Options{}); err == nil || !strings.Contains(err.Error(), "install") {
		t.Fatalf("err=%v", err)
	}
}

func TestHub_拨出模式缺少release是用法错误(t *testing.T) {
	f := newFixture(t)
	f.conf = config.Config{Mode: config.ModeDialOut, Listen: ":31418"}
	var ue install.UsageError
	if err := Run(context.Background(), f.d, Options{}); !errors.As(err, &ue) || !strings.Contains(err.Error(), "--release") {
		t.Fatalf("err=%v", err)
	}
}

const sumsTail = "0000000000000000000000000000000000000000000000000000000000000000  pimon-hub-linux-arm64\n"

func TestRelease_成功升级(t *testing.T) {
	f := newFixture(t)
	f.conf = config.Config{Mode: config.ModeDialOut, Listen: ":31418"}
	hits := &counter{}
	sums := sum("x") + "  pimon-agent-linux-arm64\n" + sum(newBin) + "  pimon-agent-linux-amd64\n" + sumsTail
	srv := httptest.NewServer(releasesHandler(hits, "v0.2.0", sums, map[string]string{"pimon-agent-linux-amd64": newBin}))
	defer srv.Close()
	f.d.ReleaseBase = srv.URL

	if err := Run(context.Background(), f.d, Options{Release: "v0.2.0"}); err != nil {
		t.Fatalf("升级失败: %v\n%s", err, f.out.String())
	}
	f.assertReplaced()
	if hits.get("/v0.2.0/SHA256SUMS") != 1 || hits.get("/v0.2.0/pimon-agent-linux-amd64") != 1 {
		t.Fatalf("请求 %v", hits.n)
	}
	if f.infoCalls != 0 {
		t.Fatal("--release 不应经 hub 取升级信息")
	}
	if len(f.runner.calls()) != 1 {
		t.Fatalf("重启 %v", f.runner.calls())
	}
}

func TestRelease_校验失败或缺少本平台不替换(t *testing.T) {
	cases := map[string]string{
		"校验和不匹配": sum("other") + "  pimon-agent-linux-amd64\n",
		"缺少本平台":  sum(newBin) + "  pimon-agent-linux-arm64\n" + sum(newBin) + "  pimon-agent-linux-amd64.sig\n",
		"格式错误":   "zz  pimon-agent-linux-amd64\n",
	}
	for name, sums := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			srv := httptest.NewServer(releasesHandler(&counter{}, "v0.2.0", sums, map[string]string{"pimon-agent-linux-amd64": newBin}))
			defer srv.Close()
			f.d.ReleaseBase = srv.URL
			if err := Run(context.Background(), f.d, Options{Release: "v0.2.0"}); err == nil {
				t.Fatal("应失败")
			}
			f.assertUnchanged()
			if len(f.runner.calls()) != 0 {
				t.Fatal("不应重启")
			}
		})
	}
}

func TestRelease_同版本或内容相同跳过(t *testing.T) {
	f := newFixture(t)
	hits := &counter{}
	srv := httptest.NewServer(releasesHandler(hits, "v0.2.0", sum(oldBin)+"  pimon-agent-linux-amd64\n", map[string]string{"pimon-agent-linux-amd64": oldBin}))
	defer srv.Close()
	f.d.ReleaseBase = srv.URL

	if err := Run(context.Background(), f.d, Options{Release: "v0.1.0"}); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), f.d, Options{Release: "v0.2.0"}); err != nil {
		t.Fatal(err)
	}
	f.assertUnchanged()
	if hits.get("/v0.2.0/pimon-agent-linux-amd64") != 0 || hits.get("/v0.1.0/SHA256SUMS") != 0 || len(f.runner.calls()) != 0 {
		t.Fatalf("不应下载二进制或重启: %v", hits.n)
	}
	if strings.Count(f.out.String(), "无需升级") != 2 {
		t.Fatalf("输出 %q", f.out.String())
	}
}

func TestRelease_经代理下载(t *testing.T) {
	f := newFixture(t)
	hits := &counter{}
	release := releasesHandler(hits, "v0.2.0", sum(newBin)+"  pimon-agent-linux-amd64\n", map[string]string{"pimon-agent-linux-amd64": newBin})
	proxied := &counter{}
	// 正向代理替身：收到绝对 URI 的请求，按目标主机记数后直接应答。
	px := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.hit(r.URL.Host)
		if r.Header.Get("Proxy-Authorization") == "" {
			http.Error(w, "need auth", http.StatusProxyAuthRequired)
			return
		}
		release.ServeHTTP(w, r)
	}))
	defer px.Close()
	f.d.ReleaseBase = "http://releases.invalid"
	proxyURL := strings.Replace(px.URL, "http://", "http://u:s3cret@", 1)

	if err := Run(context.Background(), f.d, Options{Release: "v0.2.0", Proxy: proxyURL}); err != nil {
		t.Fatalf("升级失败: %v\n%s", err, f.out.String())
	}
	f.assertReplaced()
	if proxied.get("releases.invalid") != 2 {
		t.Fatalf("代理请求 %v", proxied.n)
	}
	if strings.Contains(f.out.String(), "s3cret") {
		t.Fatalf("输出泄露代理密码: %s", f.out.String())
	}
}

func TestOptions_参数校验是用法错误(t *testing.T) {
	cases := []Options{
		{Release: "0.2.0"},
		{Release: "v0.2"},
		{Release: "v1.2.3-rc1"},
		{Release: "v1.2.3/../../x"},
		{Release: "v1.2.3\n"},
		{Proxy: "http://127.0.0.1:8080"},
		{Release: "v1.2.3", Proxy: "ftp://127.0.0.1:21"},
		{Release: "v1.2.3", Proxy: "http://127.0.0.1:8080/path"},
	}
	for _, o := range cases {
		f := newFixture(t)
		var ue install.UsageError
		if err := Run(context.Background(), f.d, o); !errors.As(err, &ue) {
			t.Fatalf("%+v: err=%v", o, err)
		}
		f.assertUnchanged()
	}
}
