package backup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sqliteHeader = "SQLite format 3\x00"

func validDB() string     { return sqliteHeader + strings.Repeat("x", 100) }
func validSecret() string { return strings.Repeat("k", 32) }

type restoreEnv struct {
	dir, db, sk string
}

func newRestoreEnv(t *testing.T) restoreEnv {
	t.Helper()
	dir := t.TempDir()
	e := restoreEnv{dir: dir, db: filepath.Join(dir, "pimon.db"), sk: filepath.Join(dir, "secret.key")}
	must := func(p, body string) {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	must(e.db, "old-db")
	must(e.db+"-wal", "old-wal")
	must(e.db+"-shm", "old-shm")
	must(e.sk, "old-secret")
	return e
}

func (e restoreEnv) assertUntouched(t *testing.T) {
	t.Helper()
	for p, want := range map[string]string{e.db: "old-db", e.db + "-wal": "old-wal", e.db + "-shm": "old-shm", e.sk: "old-secret"} {
		if b, err := os.ReadFile(p); err != nil || string(b) != want {
			t.Fatalf("%s 应保持原状: %q %v", filepath.Base(p), b, err)
		}
	}
	des, _ := os.ReadDir(e.dir)
	if len(des) != 4 {
		t.Fatalf("不应残留临时或回退文件: %v", des)
	}
}

func TestRestoreRejectsInvalidPayloads(t *testing.T) {
	cases := map[string]map[string]string{
		"密钥31字节":  {"pimon.db": validDB(), "secret.key": strings.Repeat("k", 31)},
		"密钥33字节":  {"pimon.db": validDB(), "secret.key": strings.Repeat("k", 33)},
		"数据库头不符":  {"pimon.db": "not a sqlite database at all", "secret.key": validSecret()},
		"数据库不足16": {"pimon.db": "SQLite format", "secret.key": validSecret()},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			e := newRestoreEnv(t)
			if err := Restore(makeArchive(t, files), e.db, e.sk); err == nil {
				t.Fatal("应报错")
			}
			e.assertUntouched(t)
		})
	}
}

func TestRestoreEntrySizeLimit(t *testing.T) {
	old := maxEntrySize
	maxEntrySize = 200
	t.Cleanup(func() { maxEntrySize = old })
	e := newRestoreEnv(t)
	big := sqliteHeader + strings.Repeat("x", 300)
	err := Restore(makeArchive(t, map[string]string{"pimon.db": big, "secret.key": validSecret()}), e.db, e.sk)
	if err == nil || !strings.Contains(err.Error(), "超过") {
		t.Fatalf("应因超过大小上限报错: %v", err)
	}
	e.assertUntouched(t)
}

func TestRestoreSuccessLeavesNoPreRestoreFiles(t *testing.T) {
	e := newRestoreEnv(t)
	if err := Restore(makeArchive(t, map[string]string{"pimon.db": validDB(), "secret.key": validSecret()}), e.db, e.sk); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(e.db); string(b) != validDB() {
		t.Fatal("数据库应已替换")
	}
	des, _ := os.ReadDir(e.dir)
	names := []string{}
	for _, d := range des {
		names = append(names, d.Name())
	}
	if len(names) != 2 {
		t.Fatalf("成功后只应剩 pimon.db 与 secret.key: %v", names)
	}
}

func TestRestoreReplaceFailureRollsBackToPreRestore(t *testing.T) {
	cases := map[string]func(from, to string) error{
		"密钥就位失败": func(from, to string) error {
			if strings.HasSuffix(to, "secret.key") && strings.HasSuffix(from, ".restore") {
				return errors.New("注入失败")
			}
			return os.Rename(from, to)
		},
		"数据库就位失败": func(from, to string) error {
			if strings.HasSuffix(to, "pimon.db") && strings.HasSuffix(from, ".restore") {
				return errors.New("注入失败")
			}
			return os.Rename(from, to)
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			old := rename
			rename = fn
			t.Cleanup(func() { rename = old })
			e := newRestoreEnv(t)
			if err := Restore(makeArchive(t, map[string]string{"pimon.db": validDB(), "secret.key": validSecret()}), e.db, e.sk); err == nil {
				t.Fatal("替换阶段应失败")
			}
			e.assertUntouched(t)
		})
	}
}

func TestRestoreKeepOldFilesFailureRollsBack(t *testing.T) {
	e := newRestoreEnv(t)
	// 密钥的 .pre-restore 位置被非空目录占住，保留旧密钥这一步会失败，已改名的数据库文件需回退。
	if err := os.MkdirAll(filepath.Join(e.sk+".pre-restore", "keep"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := Restore(makeArchive(t, map[string]string{"pimon.db": validDB(), "secret.key": validSecret()}), e.db, e.sk); err == nil {
		t.Fatal("应失败")
	}
	for p, want := range map[string]string{e.db: "old-db", e.db + "-wal": "old-wal", e.db + "-shm": "old-shm", e.sk: "old-secret"} {
		if b, err := os.ReadFile(p); err != nil || string(b) != want {
			t.Fatalf("%s 应回退: %q %v", filepath.Base(p), b, err)
		}
	}
	// 排除故障后重跑，应收敛到备份状态。
	if err := os.RemoveAll(e.sk + ".pre-restore"); err != nil {
		t.Fatal(err)
	}
	if err := Restore(makeArchive(t, map[string]string{"pimon.db": validDB(), "secret.key": validSecret()}), e.db, e.sk); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(e.sk); string(b) != validSecret() {
		t.Fatal("重跑后密钥应为备份内容")
	}
}
