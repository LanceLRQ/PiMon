package auth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type screenEnv struct {
	path     string
	tokens   *ScreenTokens
	sessions *Sessions
}

func newScreenEnv(t *testing.T) screenEnv {
	t.Helper()
	db := openDB(t)
	clk := newClock()
	path := filepath.Join(t.TempDir(), "screen.token")
	return screenEnv{path: path, tokens: NewScreenTokens(db, clk, path), sessions: NewSessions(db, clk)}
}

func readToken(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestScreenTokens_EnsureExists生成并幂等(t *testing.T) {
	ctx := context.Background()
	e := newScreenEnv(t)
	if err := e.tokens.EnsureExists(ctx); err != nil {
		t.Fatal(err)
	}
	first := readToken(t, e.path)
	if strings.TrimSpace(first) == "" {
		t.Fatal("文件内容为空")
	}
	if ok, err := e.tokens.Verify(ctx, first); err != nil || !ok {
		t.Fatalf("文件中的令牌应校验通过: ok=%v err=%v", ok, err)
	}
	if err := e.tokens.EnsureExists(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readToken(t, e.path); got != first {
		t.Fatal("EnsureExists 不应改变已有令牌")
	}
}

func TestScreenTokens_库有记录文件缺失时重新生成(t *testing.T) {
	ctx := context.Background()
	e := newScreenEnv(t)
	_ = e.tokens.EnsureExists(ctx)
	old := readToken(t, e.path)
	if err := os.Remove(e.path); err != nil {
		t.Fatal(err)
	}
	if err := e.tokens.EnsureExists(ctx); err != nil {
		t.Fatal(err)
	}
	fresh := readToken(t, e.path)
	if fresh == old {
		t.Fatal("应生成新令牌")
	}
	if ok, _ := e.tokens.Verify(ctx, fresh); !ok {
		t.Fatal("新令牌应与库一致")
	}
	if ok, _ := e.tokens.Verify(ctx, old); ok {
		t.Fatal("旧令牌应失效")
	}
}

func TestScreenTokens_库无记录文件存在时以库为准重新生成(t *testing.T) {
	ctx := context.Background()
	e := newScreenEnv(t)
	if err := os.WriteFile(e.path, []byte("stale-token\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := e.tokens.EnsureExists(ctx); err != nil {
		t.Fatal(err)
	}
	got := readToken(t, e.path)
	if strings.TrimSpace(got) == "stale-token" {
		t.Fatal("库无记录时应重新生成")
	}
	if ok, _ := e.tokens.Verify(ctx, got); !ok {
		t.Fatal("文件与库应一致")
	}
}

func TestScreenTokens_Verify去空白且拒绝错误值(t *testing.T) {
	ctx := context.Background()
	e := newScreenEnv(t)
	_ = e.tokens.EnsureExists(ctx)
	tok := strings.TrimSpace(readToken(t, e.path))
	if ok, _ := e.tokens.Verify(ctx, "  "+tok+"\r\n"); !ok {
		t.Fatal("应忽略首尾空白")
	}
	if ok, _ := e.tokens.Verify(ctx, tok+"x"); ok {
		t.Fatal("错误令牌不应通过")
	}
	if ok, _ := e.tokens.Verify(ctx, ""); ok {
		t.Fatal("空令牌不应通过")
	}
}

func TestScreenTokens_Verify无记录为false(t *testing.T) {
	e := newScreenEnv(t)
	if ok, err := e.tokens.Verify(context.Background(), "abc"); err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestScreenTokens_Rotate(t *testing.T) {
	ctx := context.Background()
	e := newScreenEnv(t)
	_ = e.tokens.EnsureExists(ctx)
	old := readToken(t, e.path)
	screenSess, _ := e.sessions.Create(ctx, KindScreen)
	adminSess, _ := e.sessions.Create(ctx, KindAdmin)

	if err := e.tokens.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	fresh := readToken(t, e.path)
	if fresh == old {
		t.Fatal("令牌应已更换")
	}
	if ok, _ := e.tokens.Verify(ctx, fresh); !ok {
		t.Fatal("文件内容应与库记录一致")
	}
	if ok, _ := e.tokens.Verify(ctx, old); ok {
		t.Fatal("旧令牌应失效")
	}
	if _, ok, _ := e.sessions.Lookup(ctx, screenSess); ok {
		t.Fatal("旧屏幕会话应失效")
	}
	if _, ok, _ := e.sessions.Lookup(ctx, adminSess); !ok {
		t.Fatal("管理员会话不应受影响")
	}
	st, err := os.Stat(e.path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o640 {
		t.Fatalf("文件权限 = %v, 期望 0640", st.Mode().Perm())
	}
}

func TestScreenTokens_写文件失败时旧令牌仍有效(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	clk := newClock()
	dir := t.TempDir()
	path := filepath.Join(dir, "screen.token")
	tokens := NewScreenTokens(db, clk, path)
	_ = tokens.EnsureExists(ctx)
	old := readToken(t, path)
	sessions := NewSessions(db, clk)
	sess, _ := sessions.Create(ctx, KindScreen)

	// 目标目录不可写，使原子写文件失败
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限限制")
	}
	if err := tokens.Rotate(ctx); err == nil {
		t.Fatal("写文件失败应返回错误")
	}
	if ok, _ := tokens.Verify(ctx, old); !ok {
		t.Fatal("写文件失败时旧令牌应仍有效")
	}
	if _, ok, _ := sessions.Lookup(ctx, sess); !ok {
		t.Fatal("写文件失败时屏幕会话应保留")
	}
}

func TestScreenTokens_并发Rotate后文件与库一致(t *testing.T) {
	ctx := context.Background()
	e := newScreenEnv(t)
	if err := e.tokens.EnsureExists(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.tokens.Rotate(ctx)
		}()
	}
	wg.Wait()
	ok, err := e.tokens.Verify(ctx, readToken(t, e.path))
	if err != nil || !ok {
		t.Fatalf("文件中的令牌应与库一致: ok=%v err=%v", ok, err)
	}
}

func TestScreenTokens_EnsureExists读文件失败不轮换(t *testing.T) {
	ctx := context.Background()
	e := newScreenEnv(t)
	if err := e.tokens.EnsureExists(ctx); err != nil {
		t.Fatal(err)
	}
	old := readToken(t, e.path)
	if err := os.Chmod(e.path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(e.path, 0o640) })
	if f, err := os.Open(e.path); err == nil {
		_ = f.Close()
		t.Skip("当前用户可忽略文件权限（root）")
	}
	if err := e.tokens.EnsureExists(ctx); err == nil {
		t.Fatal("读文件权限错误应原样返回")
	}
	_ = os.Chmod(e.path, 0o640)
	if got := readToken(t, e.path); got != old {
		t.Fatal("读失败不应轮换令牌")
	}
	if ok, _ := e.tokens.Verify(ctx, old); !ok {
		t.Fatal("旧令牌应仍有效")
	}
}

func TestScreenTokens_EnsureExists内容不一致时轮换(t *testing.T) {
	ctx := context.Background()
	e := newScreenEnv(t)
	_ = e.tokens.EnsureExists(ctx)
	old := readToken(t, e.path)
	if err := os.WriteFile(e.path, []byte("tampered\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := e.tokens.EnsureExists(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readToken(t, e.path); got == old || got == "tampered\n" {
		t.Fatal("内容不一致应重新生成")
	}
}
