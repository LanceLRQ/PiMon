package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
)

// withCtl 让守护进程在短路径上开控制套接字（macOS sun_path 上限 104 字节）。
func withCtl(t *testing.T) (option, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "pctl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "agent.sock")
	return func(c *Config) { c.CtlSock = path }, path
}

func TestCtl_拨入状态经控制套接字输出且不含凭据(t *testing.T) {
	hub := startStubHub(t, nil)
	opt, sock := withCtl(t)
	h := newHarness(t, hub.config(), opt).start()
	waitUntil(t, "连上", func() bool { return h.status().Connected })
	c := ctl.Client{Path: sock}
	var raw json.RawMessage
	must(t, c.Do(context.Background(), ctl.Request{Op: ctl.OpStatus}, &raw))
	if strings.Contains(string(raw), hub.cred) || strings.Contains(string(raw), "credential") {
		t.Fatalf("状态含凭据: %s", raw)
	}
	st, err := c.Status(context.Background())
	must(t, err)
	if st.Mode != config.ModeDialIn || !st.Connected || st.HubURL != hub.srv.URL || st.Pin != hub.srv.Pin.String() {
		t.Fatalf("状态 = %+v", st)
	}
	_, err = c.Pair(context.Background())
	var re *ctl.RemoteError
	if !errors.As(err, &re) || !strings.Contains(re.Msg, "拨入模式请用 install --code 重新配对") {
		t.Fatalf("拨入模式 pair 应报错: %v", err)
	}
}

func TestCtl_拨出状态不含哈希且配对码不进状态(t *testing.T) {
	opt, sock := withCtl(t)
	h := newHarness(t, &config.Config{Mode: config.ModeDialOut, CredentialHash: strings.Repeat("ab", 32)}, opt).start()
	h.listenAddr()
	c := ctl.Client{Path: sock}
	w, err := c.Pair(context.Background())
	must(t, err)
	if w.Code == "" || w.Port == 0 || !strings.HasPrefix(w.Fingerprint, "SHA256:") {
		t.Fatalf("pair = %+v", w)
	}
	var raw json.RawMessage
	must(t, c.Do(context.Background(), ctl.Request{Op: ctl.OpStatus}, &raw))
	for _, s := range []string{w.Code, strings.Repeat("ab", 32), "abababab"} {
		if strings.Contains(string(raw), s) {
			t.Fatalf("状态含敏感值 %q: %s", s, raw)
		}
	}
	if ringContains(h, w.Code) {
		t.Fatal("配对码进了日志")
	}
	if _, err := c.UpgradeInfo(context.Background()); err == nil || !strings.Contains(err.Error(), "未连接 hub") {
		t.Fatalf("无会话时应报未连接: %v", err)
	}
	must(t, c.Reload(context.Background()))
}

func TestCtl_套接字已被占用时Run返回错误(t *testing.T) {
	opt, sock := withCtl(t)
	newHarness(t, &config.Config{Mode: config.ModeDialOut}, opt).start().listenAddr()
	h2 := newHarness(t, &config.Config{Mode: config.ModeDialOut}, func(c *Config) { c.CtlSock = sock })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // 只为红灯阶段不挂住
	defer cancel()
	if err := h2.d.Run(ctx); err == nil || !strings.Contains(err.Error(), "控制套接字") {
		t.Fatalf("第二个守护进程应因控制套接字被占用而失败: %v", err)
	}
	if _, err := os.Stat(h2.paths.DB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应在打开本地数据库之前失败: %v", err)
	}
}

func TestCtl_退出后删除套接字文件(t *testing.T) {
	opt, sock := withCtl(t)
	h := newHarness(t, &config.Config{Mode: config.ModeDialOut}, opt).start()
	h.listenAddr()
	if fi, err := os.Lstat(sock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("套接字 = %v %v", fi, err)
	}
	h.stop()
	if _, err := os.Lstat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("退出后应删除套接字: %v", err)
	}
}
