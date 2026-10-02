package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
	"github.com/LanceLRQ/PiMon/src/internal/hub/config"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
)

func TestCheckRoot_仅在root且目录属主非root时拒绝(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir()}
	owner := func(uid uint32) func(string) (uint32, error) {
		return func(string) (uint32, error) { return uid, nil }
	}
	cases := []struct {
		name    string
		euid    int
		ownerFn func(string) (uint32, error)
		refuse  bool
	}{
		{"非 root 运行", 1000, owner(995), false},
		{"root 运行且目录属主为 root", 0, owner(0), false},
		{"root 运行且目录属主非 root", 0, owner(995), true},
	}
	for _, c := range cases {
		err := checkRoot(cfg, c.euid, c.ownerFn)
		if (err != nil) != c.refuse {
			t.Errorf("%s: err = %v, 期望拒绝=%v", c.name, err, c.refuse)
		}
		if err != nil && !strings.Contains(err.Error(), "sudo -u pimon") {
			t.Errorf("%s: 提示应包含 sudo -u pimon: %v", c.name, err)
		}
	}
}

func TestCheckRoot_数据目录不存在不拒绝(t *testing.T) {
	cfg := config.Config{DataDir: filepath.Join(t.TempDir(), "missing")}
	if err := checkRoot(cfg, 0, statOwner); err != nil {
		t.Fatalf("目录不存在时不应拒绝: %v", err)
	}
}

func TestCheckRoot_其他stat错误原样返回(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir()}
	boom := errors.New("boom")
	if err := checkRoot(cfg, 0, func(string) (uint32, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestStatOwner返回真实属主(t *testing.T) {
	uid, err := statOwner(t.TempDir())
	if err != nil || uid != uint32(os.Getuid()) {
		t.Fatalf("uid = %d err = %v", uid, err)
	}
}

func TestLockDataDir_持有期间restore被拒绝(t *testing.T) {
	cfg := testConfig(t)
	lk, err := LockDataDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = Restore(cfg, "/nonexistent.tar.gz", &out)
	if !errors.Is(err, ErrDataDirBusy) {
		t.Fatalf("err = %v，期望 ErrDataDirBusy", err)
	}
	if _, err := LockDataDir(cfg); !errors.Is(err, ErrDataDirBusy) {
		t.Fatalf("第二次 LockDataDir err = %v", err)
	}
	_ = lk.Release()
	if err := Restore(cfg, "/nonexistent.tar.gz", &out); errors.Is(err, ErrDataDirBusy) || err == nil {
		t.Fatalf("释放后应走到打开备份包的错误: %v", err)
	}
}

func TestLockDataDir_目录不存在时创建(t *testing.T) {
	cfg := testConfig(t)
	lk, err := LockDataDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lk.Release() }()
	fi, err := os.Stat(cfg.DataDir)
	if err != nil || fi.Mode().Perm() != 0o750 {
		t.Fatalf("目录应以 0750 创建: %v %v", fi, err)
	}
}

func TestOpen_降级检测先于升级前备份(t *testing.T) {
	cfg := testConfig(t)
	first, err := Open(context.Background(), cfg, testOpts(WithVersion("2.0.0"))...)
	if err != nil {
		t.Fatal(err)
	}
	emb, _ := store.EmbeddedVersion()
	if _, err := first.db.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, 'future')`, emb+1); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	// 旧二进制（版本字符串不同）打开新库：应直接报 ErrDatabaseNewer，不生成 pre-upgrade 备份。
	_, err = Open(context.Background(), cfg, testOpts(WithVersion("1.0.0"))...)
	if !errors.Is(err, store.ErrDatabaseNewer) {
		t.Fatalf("err = %v，期望 ErrDatabaseNewer", err)
	}
	list, lerr := backup.New(backup.Config{Dir: cfg.BackupDir()}).List()
	if lerr != nil || len(list) != 0 {
		t.Fatalf("不应产生备份: %+v %v", list, lerr)
	}
}
