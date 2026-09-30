package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

type fixture struct {
	dir        string
	dbPath     string
	secretPath string
	backupDir  string
	db         *store.DB
	clk        *clock.Fake
	svc        *Service
	settings   model.Settings
	mu         sync.Mutex
}

func newFixture(t *testing.T, start time.Time) *fixture {
	t.Helper()
	f := &fixture{dir: t.TempDir()}
	f.dbPath = filepath.Join(f.dir, "pimon.db")
	f.secretPath = filepath.Join(f.dir, "secret.key")
	f.backupDir = filepath.Join(f.dir, "backups")
	f.settings = model.Settings{Timezone: "UTC", Backup: model.BackupSettings{DailyAt: "04:00", Keep: 3}}
	if _, err := secret.LoadOrCreate(f.secretPath); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t (v TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t (v) VALUES ('before')`); err != nil {
		t.Fatal(err)
	}
	f.db = db
	f.clk = clock.NewFake(start)
	f.svc = New(Config{
		DB: db, Clock: f.clk, SecretPath: f.secretPath, Dir: f.backupDir,
		Settings: func() model.Settings {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.settings
		},
	})
	return f
}

var start0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func tarEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fh.Close() }()
	gz, err := gzip.NewReader(fh)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg {
			t.Fatalf("条目 %s 不是普通文件", h.Name)
		}
		b, _ := io.ReadAll(tr)
		out[h.Name] = b
	}
}

func TestCreateContainsExactlyTwoFiles(t *testing.T) {
	f := newFixture(t, start0)
	info, err := f.svc.Create(context.Background(), ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "pimon-backup-20260101-000000-manual.tar.gz" || info.Reason != ReasonManual {
		t.Fatalf("info = %+v", info)
	}
	if !info.CreatedAt.Equal(start0) || info.Size <= 0 {
		t.Fatalf("info = %+v", info)
	}
	path := filepath.Join(f.backupDir, info.Name)
	entries := tarEntries(t, path)
	if len(entries) != 2 || len(entries["pimon.db"]) == 0 || len(entries["secret.key"]) != secret.KeySize {
		t.Fatalf("条目不对: %v", len(entries))
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 || st.Size() != info.Size {
		t.Fatalf("权限/大小不对: %v %d", st.Mode().Perm(), st.Size())
	}
	// 备份目录中不应残留临时文件
	des, _ := os.ReadDir(f.backupDir)
	if len(des) != 1 {
		t.Fatalf("目录残留文件: %v", des)
	}
}

func TestCreateSameSecondSameReasonFails(t *testing.T) {
	f := newFixture(t, start0)
	if _, err := f.svc.Create(context.Background(), ReasonManual); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(context.Background(), ReasonManual); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v，期望 ErrExists", err)
	}
	if _, err := f.svc.Create(context.Background(), ReasonDaily); err != nil {
		t.Fatalf("不同原因应可创建: %v", err)
	}
}

func TestCreateRejectsUnknownReason(t *testing.T) {
	f := newFixture(t, start0)
	if _, err := f.svc.Create(context.Background(), "weird"); err == nil {
		t.Fatal("未知原因应报错")
	}
}

func TestCreateConcurrent(t *testing.T) {
	f := newFixture(t, start0)
	var wg sync.WaitGroup
	for _, r := range []string{ReasonManual, ReasonDaily, ReasonPreUpgrade} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.svc.Create(context.Background(), r); err != nil {
				t.Errorf("Create(%s): %v", r, err)
			}
		}()
	}
	wg.Wait()
	list, err := f.svc.List()
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %v, err = %v", list, err)
	}
}

func TestListSortedAndFiltered(t *testing.T) {
	f := newFixture(t, start0)
	ctx := context.Background()
	if list, err := f.svc.List(); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("空目录应返回非 nil 空切片: %v %v", list, err)
	}
	for i := 0; i < 3; i++ {
		if _, err := f.svc.Create(ctx, ReasonDaily); err != nil {
			t.Fatal(err)
		}
		f.clk.Advance(time.Hour)
	}
	if err := os.WriteFile(filepath.Join(f.backupDir, "note.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := f.svc.List()
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %v, err = %v", list, err)
	}
	if !list[0].CreatedAt.After(list[1].CreatedAt) || !list[1].CreatedAt.After(list[2].CreatedAt) {
		t.Fatalf("应按时间倒序: %v", list)
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	f := newFixture(t, start0)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := f.svc.Create(ctx, ReasonDaily); err != nil {
			t.Fatal(err)
		}
		f.clk.Advance(time.Hour)
	}
	if err := f.svc.Prune(2); err != nil {
		t.Fatal(err)
	}
	list, _ := f.svc.List()
	if len(list) != 2 {
		t.Fatalf("剩 %d 份", len(list))
	}
	if !list[0].CreatedAt.Equal(start0.Add(4*time.Hour)) || !list[1].CreatedAt.Equal(start0.Add(3*time.Hour)) {
		t.Fatalf("应保留最新两份: %v", list)
	}
}

func TestOpen(t *testing.T) {
	f := newFixture(t, start0)
	info, _ := f.svc.Create(context.Background(), ReasonManual)
	fh, got, err := f.svc.Open(info.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fh.Close() }()
	if got.Name != info.Name || got.Size != info.Size {
		t.Fatalf("got = %+v", got)
	}
	for _, bad := range []string{
		"../secret.key", "pimon-backup-20260101-000000-manual.tar.gz/../x", "/etc/passwd",
		"pimon-backup-20260101-000000-evil.tar.gz", "note.txt", "",
		"pimon-backup-20260102-000000-manual.tar.gz", // 合法格式但不存在
	} {
		if _, _, err := f.svc.Open(bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("Open(%q) err = %v，期望 ErrNotFound", bad, err)
		}
	}
}

func TestNextRun(t *testing.T) {
	utc := time.UTC
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("无时区数据库")
	}
	cases := []struct {
		name string
		now  time.Time
		loc  *time.Location
		at   string
		want time.Duration
	}{
		{"22:47 到次日 04:00", time.Date(2026, 1, 1, 22, 47, 0, 0, utc), utc, "04:00", 5*time.Hour + 13*time.Minute},
		{"恰好 04:00 等 24 小时", time.Date(2026, 1, 1, 4, 0, 0, 0, utc), utc, "04:00", 24 * time.Hour},
		{"当天稍早", time.Date(2026, 1, 1, 3, 0, 0, 0, utc), utc, "04:00", time.Hour},
		{"跨时区：UTC 19:00 即上海 03:00", time.Date(2026, 1, 1, 19, 0, 0, 0, utc), sh, "04:00", time.Hour},
		{"跨时区：UTC 20:00 即上海 04:00 整", time.Date(2026, 1, 1, 20, 0, 0, 0, utc), sh, "04:00", 24 * time.Hour},
	}
	for _, c := range cases {
		got, err := NextRun(c.now, c.loc, c.at)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got.Sub(c.now) != c.want {
			t.Errorf("%s: 相差 %v，期望 %v", c.name, got.Sub(c.now), c.want)
		}
	}
	for _, bad := range []string{"", "4:00x", "25:00", "12:60", "abc"} {
		if _, err := NextRun(start0, utc, bad); err == nil {
			t.Errorf("NextRun(%q) 应报错", bad)
		}
	}
}

// waitFor 用 Gosched 让出调度直到条件成立，不 sleep；超时则失败。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		runtime.Gosched()
	}
}

func TestRunDailyCreatesAndPrunes(t *testing.T) {
	f := newFixture(t, start0)
	f.settings.Backup.Keep = 2
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.svc.RunDaily(ctx); close(done) }()

	waitFor(t, "进入等待", func() bool { return f.clk.Waiters() == 1 })
	f.clk.Advance(4 * time.Hour) // 到 04:00
	waitFor(t, "第一次备份完成并再次等待", func() bool { return f.clk.Waiters() == 1 })
	list, _ := f.svc.List()
	if len(list) != 1 || list[0].Reason != ReasonDaily {
		t.Fatalf("list = %v", list)
	}
	for i := 0; i < 2; i++ {
		f.clk.Advance(24 * time.Hour)
		waitFor(t, "下一轮等待", func() bool { return f.clk.Waiters() == 1 })
	}
	list, _ = f.svc.List()
	if len(list) != 2 {
		t.Fatalf("应按 keep=2 清理，实际 %d 份", len(list))
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消后 RunDaily 应返回")
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	f := newFixture(t, start0)
	info, err := f.svc.Create(context.Background(), ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE t SET v = 'after'`); err != nil {
		t.Fatal(err)
	}

	newDir := filepath.Join(t.TempDir(), "restored")
	newDB := filepath.Join(newDir, "pimon.db")
	newSecret := filepath.Join(newDir, "secret.key")
	// 预置旧的 wal/shm，恢复后应被删除
	if err := os.MkdirAll(newDir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, suf := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(newDB+suf, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Restore(filepath.Join(f.backupDir, info.Name), newDB, newSecret); err != nil {
		t.Fatal(err)
	}
	for _, suf := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(newDB + suf); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s 应被删除: %v", suf, err)
		}
	}
	rdb, err := store.Open(newDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rdb.Close() }()
	var v string
	if err := rdb.QueryRow(`SELECT v FROM t`).Scan(&v); err != nil || v != "before" {
		t.Fatalf("v = %q, err = %v", v, err)
	}
	orig, _ := os.ReadFile(f.secretPath)
	got, _ := os.ReadFile(newSecret)
	if string(orig) != string(got) {
		t.Fatal("密钥应一致")
	}
}

func makeArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.tar.gz")
	fh, _ := os.Create(p)
	gz := gzip.NewWriter(fh)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = fh.Close()
	return p
}

func TestRestoreRejectsBadArchives(t *testing.T) {
	cases := map[string]map[string]string{
		"未知文件":  {"pimon.db": "a", "secret.key": "b", "evil": "c"},
		"路径穿越":  {"../pimon.db": "a", "secret.key": "b"},
		"缺密钥":   {"pimon.db": "a"},
		"缺数据库":  {"secret.key": "b"},
		"带目录前缀": {"x/pimon.db": "a", "secret.key": "b"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "pimon.db")
			sk := filepath.Join(dir, "secret.key")
			if err := os.WriteFile(db, []byte("orig-db"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Restore(makeArchive(t, files), db, sk); err == nil {
				t.Fatal("应报错")
			}
			if b, _ := os.ReadFile(db); string(b) != "orig-db" {
				t.Fatal("失败时不得改动原数据库")
			}
			if _, err := os.Stat(sk); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("失败时不得写入密钥")
			}
			if des, _ := os.ReadDir(dir); len(des) != 1 {
				t.Fatalf("失败时不得残留临时文件: %v", des)
			}
		})
	}
}
