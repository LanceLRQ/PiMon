package install

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestOSFSWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bin")
	var fsys osFS
	if err := fsys.WriteFile(target, []byte("v1"), 0o755, nil); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile(target, []byte("v2-longer"), 0o755, &Owner{UID: os.Getuid(), GID: os.Getgid()}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	fi, err := fsys.Stat(target)
	if string(b) != "v2-longer" || err != nil || fi.Mode != 0o755 || fi.UID != os.Getuid() {
		t.Fatalf("content=%q fi=%+v err=%v", b, fi, err)
	}
	names, _ := fsys.ReadDir(dir)
	if len(names) != 1 {
		t.Fatalf("不应残留临时文件: %v", names)
	}
}

func TestOSFSWriteFileFailureLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	// 目标是个目录，rename 会失败。
	target := filepath.Join(dir, "d")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (osFS{}).WriteFile(target, []byte("x"), 0o644, nil); err == nil {
		t.Fatal("期望失败")
	}
	names, _ := (osFS{}).ReadDir(dir)
	if len(names) != 1 {
		t.Fatalf("失败后应清理临时文件: %v", names)
	}
}

func TestOSFSMkdirAndFix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	own := Owner{UID: os.Getuid(), GID: os.Getgid()}
	var fsys osFS
	if err := fsys.Mkdir(dir, 0o750, own); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsys.SetOwnerMode(dir, 0o750, own); err != nil {
		t.Fatal(err)
	}
	fi, err := fsys.Stat(dir)
	if err != nil || !fi.IsDir || fi.Mode != 0o750 {
		t.Fatalf("%+v %v", fi, err)
	}
}

func TestProcPortsListeners(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hdr := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	write("net/tcp", hdr+
		"   0: 00000000:7AB7 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 4242 1 0 100 0 0 10 0\n"+
		"   1: 0100007F:2243 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 9999 1 0 100 0 0 10 0\n"+
		"   2: 0A16210A:7AB7 0A16210B:C000 01 00000000:00000000 00:00000000 00000000     0        0 5555 1 0 100 0 0 10 0\n")
	// 31415 = 0x7AB7；没有 net/tcp6：无 IPv6 的系统不应报错。
	if err := os.MkdirAll(filepath.Join(root, "321", "fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[4242]", filepath.Join(root, "321", "fd", "7")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/local/bin/pimon-hub (deleted)", filepath.Join(root, "321", "exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "322", "fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[9999]", filepath.Join(root, "322", "fd", "3")); err != nil {
		t.Fatal(err)
	}
	got, err := ProcPorts{Root: root}.Listeners(31415)
	if err != nil || len(got) != 1 || got[0].PID != 321 || got[0].Exe != "/usr/local/bin/pimon-hub (deleted)" {
		t.Fatalf("got %+v err=%v", got, err)
	}
	if got, err := (ProcPorts{Root: root}).Listeners(8080); err != nil || len(got) != 0 {
		t.Fatalf("空闲端口: %+v %v", got, err)
	}
	// 监听 socket 找不到属主（无权读 /proc/<pid>/fd 等）时给出未知属主。
	if err := os.Remove(filepath.Join(root, "321", "fd", "7")); err != nil {
		t.Fatal(err)
	}
	got, err = ProcPorts{Root: root}.Listeners(31415)
	if err != nil || len(got) != 1 || got[0].PID != 0 {
		t.Fatalf("未知属主: %+v %v", got, err)
	}
}

func TestProcPortsMissingTCPIsError(t *testing.T) {
	if _, err := (ProcPorts{Root: t.TempDir()}).Listeners(1); err == nil {
		t.Fatal("缺少 /proc/net/tcp 应报错")
	}
}

func healthHandler(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
}

func TestHTTPHealthPlain(t *testing.T) {
	srv := httptest.NewServer(healthHandler(http.StatusOK))
	defer srv.Close()
	if err := httpHealth(context.Background(), srv.URL+"/healthz"); err != nil {
		t.Fatal(err)
	}
}

// hub 开启直连 HTTPS 时，同一端口对明文请求回 400，需要用 https 再探一次。
func TestHTTPHealthFallsBackToHTTPSWithSelfSignedCert(t *testing.T) {
	srv := httptest.NewTLSServer(healthHandler(http.StatusOK))
	defer srv.Close()
	plain := "http" + srv.URL[len("https"):] + "/healthz"
	if err := httpHealth(context.Background(), plain); err != nil {
		t.Fatalf("HTTPS 模式应判为就绪: %v", err)
	}
}

func TestHTTPHealthBothFail(t *testing.T) {
	srv := httptest.NewServer(healthHandler(http.StatusServiceUnavailable))
	defer srv.Close()
	if err := httpHealth(context.Background(), srv.URL+"/healthz"); err == nil {
		t.Fatal("明文 503 且 https 不可用应判为未就绪")
	}
	tlsSrv := httptest.NewTLSServer(healthHandler(http.StatusServiceUnavailable))
	defer tlsSrv.Close()
	if err := httpHealth(context.Background(), "http"+tlsSrv.URL[len("https"):]+"/healthz"); err == nil {
		t.Fatal("https 返回 503 也应判为未就绪")
	}
}
