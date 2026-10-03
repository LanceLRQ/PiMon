package upgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
)

const (
	oldBin = "old-binary-content"
	newBin = "new-binary-content-v0.2.0"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// fakeRunner 记录执行过的命令。
type fakeRunner struct {
	mu   sync.Mutex
	cmds [][]string
	err  error
}

func (r *fakeRunner) Run(_ context.Context, argv ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, argv)
	return "", r.err
}

func (r *fakeRunner) RunEnv(ctx context.Context, _ []string, argv ...string) (string, error) {
	return r.Run(ctx, argv...)
}

func (r *fakeRunner) calls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.cmds)
}

// fixture 是一次升级的环境：二进制放在临时目录，所有外部依赖都是替身。
type fixture struct {
	t         *testing.T
	dir       string
	bin       string
	out       bytes.Buffer
	runner    *fakeRunner
	conf      config.Config
	info      ctl.UpgradeInfo
	infoErr   error
	infoCalls int
	d         Deps
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin", "pimon-agent")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte(oldBin), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, dir: dir, bin: bin, runner: &fakeRunner{}}
	f.conf = config.Config{Mode: config.ModeDialIn, HubURL: "wss://127.0.0.1:31418", HubPin: "SHA256:" + strings.Repeat("A", 43)}
	f.info = ctl.UpgradeInfo{HubVersion: "v0.2.0", SHA256: sum(newBin)}
	f.d = Deps{
		Out: &f.out, GOOS: "linux", GOARCH: "amd64", EUID: 1000, Version: "v0.1.0",
		Paths:      config.Paths{Bin: bin, Config: filepath.Join(dir, "agent.json"), CtlSock: filepath.Join(dir, "agent.sock")},
		LoadConfig: func() (config.Config, error) { return f.conf, nil },
		UpgradeInfo: func(context.Context) (ctl.UpgradeInfo, error) {
			f.infoCalls++
			return f.info, f.infoErr
		},
		Runner:      f.runner,
		ReleaseBase: "http://127.0.0.1:1/never",
		MaxSize:     DefaultMaxSize,
	}
	return f
}

// useHub 把 upgrade_info 的 hub 主机与网页端口指向 srv。
func (f *fixture) useHub(srv *httptest.Server) {
	u, err := url.Parse(srv.URL)
	if err != nil {
		f.t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(u.Host)
	f.info.HubHost = host
	f.info.WebPort, _ = strconv.Atoi(port)
}

// pinTo 把配置里的钉扎指纹设为 srv 的证书。
func (f *fixture) pinTo(srv *httptest.Server) {
	pin, err := certs.SPKIOf(srv.TLS.Certificates[0])
	if err != nil {
		f.t.Fatal(err)
	}
	f.conf.HubPin = pin.String()
}

// assertUnchanged 断言原二进制未被改动且目录里没有临时文件残留。
func (f *fixture) assertUnchanged() {
	f.t.Helper()
	b, err := os.ReadFile(f.bin)
	if err != nil || string(b) != oldBin {
		f.t.Fatalf("原二进制被改动: %q err=%v", b, err)
	}
	f.assertNoTemp()
}

func (f *fixture) assertNoTemp() {
	f.t.Helper()
	ents, err := os.ReadDir(filepath.Dir(f.bin))
	if err != nil {
		f.t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != "pimon-agent" {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		f.t.Fatalf("二进制目录有残留: %v", names)
	}
}

func (f *fixture) assertReplaced() {
	f.t.Helper()
	b, err := os.ReadFile(f.bin)
	if err != nil || string(b) != newBin {
		f.t.Fatalf("二进制未替换: %q err=%v", b, err)
	}
	fi, err := os.Stat(f.bin)
	if err != nil || fi.Mode().Perm() != 0o755 {
		f.t.Fatalf("权限 %v err=%v", fi.Mode().Perm(), err)
	}
	f.assertNoTemp()
}

// counter 是并发安全的计数器。
type counter struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *counter) hit(k string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = map[string]int{}
	}
	c.n[k]++
}

func (c *counter) get(k string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[k]
}

// hubHandler 提供 /agent/download/<平台>，内容为 body。
func hubHandler(hits *counter, platform, body string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent/download/{p}", func(w http.ResponseWriter, r *http.Request) {
		hits.hit(r.URL.Path)
		if r.PathValue("p") != platform {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write([]byte(body))
	})
	return mux
}

// releasesHandler 提供 /<tag>/SHA256SUMS 与 /<tag>/pimon-agent-<平台>。
func releasesHandler(hits *counter, tag, sums string, assets map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.hit(r.URL.Path)
		name, ok := strings.CutPrefix(r.URL.Path, "/"+tag+"/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		if name == "SHA256SUMS" {
			_, _ = w.Write([]byte(sums))
			return
		}
		body, ok := assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	})
}
