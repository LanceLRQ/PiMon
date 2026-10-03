package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LanceLRQ/PiMon/src/internal/hub/agentdist"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

// fakeBinary 是假的「agent」：把收到的参数逐行写到 $PIMON_TEST_ARGS 指向的文件。
const fakeBinary = "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\"; done > \"$PIMON_TEST_ARGS\"\n"

func gzipBytes(raw []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	return buf.Bytes()
}

// distFS 构造内嵌数据：每个平台的 .gz 内容为 served，SHA256SUMS 里记录的是 summed 的校验和。
func distFS(served, summed []byte, platforms ...string) fstest.MapFS {
	h := sha256.Sum256(summed)
	fsys := fstest.MapFS{"VERSION": {Data: []byte("vtest\n")}}
	sums := ""
	for _, p := range platforms {
		fsys["pimon-agent-"+p+".gz"] = &fstest.MapFile{Data: gzipBytes(served)}
		sums += hex.EncodeToString(h[:]) + "  pimon-agent-" + p + "\n"
	}
	fsys["SHA256SUMS"] = &fstest.MapFile{Data: []byte(sums)}
	return fsys
}

// hubWith 起一个带指定内嵌分发数据的 hub 路由。
func hubWith(t *testing.T, fsys fstest.MapFS) *httptest.Server {
	t.Helper()
	old := version.Version
	version.Version = "vtest"
	t.Cleanup(func() { version.Version = old })
	d := newEnv(t).deps
	d.AgentDist = agentdist.NewFS(fsys)
	srv := httptest.NewServer(New(d))
	t.Cleanup(srv.Close)
	return srv
}

func distServer(t *testing.T, raw []byte, platforms ...string) *httptest.Server {
	return hubWith(t, distFS(raw, raw, platforms...))
}

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestAgent下载返回原始字节与校验和(t *testing.T) {
	raw := []byte("fake agent bytes \x00\x01\x02")
	srv := distServer(t, raw, "linux-amd64")
	resp, body := get(t, srv.URL+"/agent/download/linux-amd64")
	if resp.StatusCode != 200 || !bytes.Equal(body, raw) {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="pimon-agent-linux-amd64"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if resp.ContentLength != int64(len(raw)) {
		t.Errorf("Content-Length = %d", resp.ContentLength)
	}
	_, sums := get(t, srv.URL+"/agent/checksums")
	h := sha256.Sum256(raw)
	if want := hex.EncodeToString(h[:]) + "  pimon-agent-linux-amd64\n"; string(sums) != want {
		t.Errorf("checksums = %q，期望 %q", sums, want)
	}
}

func TestAgent未知平台404与错误码(t *testing.T) {
	srv := distServer(t, []byte("x"), "linux-amd64")
	for _, p := range []string{"linux-arm64", "windows-amd64", "..%2fetc", "linux-amd64.gz"} {
		resp, body := get(t, srv.URL+"/agent/download/"+p)
		if resp.StatusCode != 404 || !strings.Contains(string(body), `"agent.binary_unavailable"`) {
			t.Errorf("%s: status=%d body=%s", p, resp.StatusCode, body)
		}
	}
}

func TestAgentDist为nil时一律404(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/agent/download/linux-amd64", "/agent/checksums"} {
		resp, body := get(t, e.srv.URL+p)
		if resp.StatusCode != 404 || !strings.Contains(string(body), `"agent.binary_unavailable"`) {
			t.Errorf("%s: status=%d body=%s", p, resp.StatusCode, body)
		}
	}
}

func TestInstallScript语法与Host注入(t *testing.T) {
	srv := distServer(t, []byte("x"), "linux-amd64")
	// 没有任何会话的客户端即可取得脚本（免登录）。
	resp, body := get(t, srv.URL+"/agent/install.sh")
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `HUB="`+srv.URL+`"`) {
		t.Errorf("脚本里没有本次请求的地址 %s", srv.URL)
	}
	if strings.Contains(string(body), "@HUB_URL@") {
		t.Error("占位符未替换")
	}
	// 一行安装是非交互模式，用法注释须说明 docker 组与 sudoers 都要显式加参数。
	for _, flag := range []string{"--docker-group", "--sudoers"} {
		if !strings.Contains(string(body), flag) {
			t.Errorf("脚本用法注释缺少 %s", flag)
		}
	}
	script := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(script, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-n", script).CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s", err, out)
	}

	for _, host := range []string{`x";touch /tmp/pwn;"`, "a b", "evil$(id)", "a'b"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/agent/install.sh", nil)
		req.Host = host
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if r.StatusCode != 400 {
			t.Errorf("Host %q: status=%d body=%s", host, r.StatusCode, b)
		}
	}
}

func currentPlatform(t *testing.T) string {
	t.Helper()
	p := runtime.GOOS + "-" + runtime.GOARCH
	switch p {
	case "linux-amd64", "linux-arm64", "darwin-arm64":
		return p
	}
	t.Skipf("不支持的测试平台 %s", p)
	return ""
}

type installRun struct {
	out  string
	err  error
	args string // 假 install 收到的参数（每行一个），未执行时为空
	home string
	// tmpLeft 是脚本结束后 TMPDIR 中残留的条目数，应为 0
	tmpLeft int
}

// runInstall 在临时 HOME 下用 --user 跑脚本，不触碰真实 /usr/local 与 ~/.pimon。
func runInstall(t *testing.T, srv *httptest.Server, args ...string) installRun {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("没有 curl")
	}
	_, body := get(t, srv.URL+"/agent/install.sh")
	dir := t.TempDir()
	script := filepath.Join(dir, "install.sh")
	if err := os.WriteFile(script, body, 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(dir, "args")
	tmpdir := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmpdir, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), "HOME="+home, "PIMON_TEST_ARGS="+argsFile, "TMPDIR="+tmpdir)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	got, _ := os.ReadFile(argsFile)
	left, _ := os.ReadDir(tmpdir)
	return installRun{out: string(out), err: err, args: string(got), home: home, tmpLeft: len(left)}
}

func TestInstallScript下载校验并执行install(t *testing.T) {
	srv := distServer(t, []byte(fakeBinary), currentPlatform(t))
	r := runInstall(t, srv, "--user", "--hub", "wss://1.2.3.4:31418", "--code", "ABC", "--sudoers")
	if r.err != nil {
		t.Fatalf("脚本失败：%v\n%s", r.err, r.out)
	}
	if want := "install\n--user\n--hub\nwss://1.2.3.4:31418\n--code\nABC\n--sudoers\n"; r.args != want {
		t.Errorf("install 收到的参数 = %q，期望 %q", r.args, want)
	}
	if r.tmpLeft != 0 {
		t.Errorf("成功后临时目录未清理，残留 %d 项", r.tmpLeft)
	}
	bin := filepath.Join(r.home, ".pimon", "bin", "pimon-agent")
	if st, err := os.Stat(bin); err != nil || st.Mode().Perm()&0o100 == 0 {
		t.Errorf("二进制未放到 %s：%v", bin, err)
	}
}

func TestInstallScript校验失败时退出(t *testing.T) {
	platform := currentPlatform(t)
	// 校验和记的是原始内容，实际提供下载的是被篡改的另一份。
	tampered := []byte("#!/bin/sh\necho tampered > \"$PIMON_TEST_ARGS\"\n")
	srv := hubWith(t, distFS(tampered, []byte(fakeBinary), platform))
	r := runInstall(t, srv, "--user")
	if r.err == nil {
		t.Fatalf("校验失败应非零退出\n%s", r.out)
	}
	if !strings.Contains(r.out, "校验失败") {
		t.Errorf("输出应说明原因：%s", r.out)
	}
	if r.tmpLeft != 0 {
		t.Errorf("失败后临时目录未清理，残留 %d 项", r.tmpLeft)
	}
	if r.args != "" {
		t.Errorf("校验失败后不应执行 install：%q", r.args)
	}
	if _, err := os.Stat(filepath.Join(r.home, ".pimon")); err == nil {
		t.Error("校验失败后不应放置任何文件")
	}
}

func TestInstallScript平台缺失时退出(t *testing.T) {
	srv := distServer(t, []byte(fakeBinary)) // 没有任何平台
	r := runInstall(t, srv, "--user")
	if r.err == nil || !strings.Contains(r.out, "安装失败") {
		t.Fatalf("应失败并说明原因：err=%v\n%s", r.err, r.out)
	}
}
