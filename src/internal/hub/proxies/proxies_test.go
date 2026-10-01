package proxies

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/secret"
	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy/proxytest"
)

// fakeReferrers 是 Referrers 的测试替身。
type fakeReferrers struct {
	mu     sync.Mutex
	refs   map[string][]model.ProxyReferrer
	resets []string
}

func (f *fakeReferrers) ListByProxy(_ context.Context, id string) ([]model.ProxyReferrer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.ProxyReferrer(nil), f.refs[id]...), nil
}

func (f *fakeReferrers) ResetToDirect(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resets = append(f.resets, id)
	delete(f.refs, id)
	return nil
}

// stepClock 每次 Now 前进固定步长，使延迟可断言且不 sleep。
type stepClock struct {
	clock.Clock
	mu   sync.Mutex
	t    time.Time
	step time.Duration
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(c.step)
	return c.t
}

type fixture struct {
	s    *Store
	db   *store.DB
	refs *fakeReferrers
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	refs := &fakeReferrers{refs: map[string][]model.ProxyReferrer{}}
	clk := &stepClock{Clock: clock.NewFake(time.Unix(0, 0)), t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), step: 7 * time.Millisecond}
	return &fixture{s: New(Config{DB: db, Box: box, Clock: clk, Referrers: refs}), db: db, refs: refs}
}

func input(name string) model.ProxyInput {
	return model.ProxyInput{Name: name, Scheme: "http", Address: "127.0.0.1:8080"}
}

func TestCreateGetList(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	got, err := f.s.Create(ctx, model.ProxyInput{Name: " 办公室 ", Scheme: "socks5", Address: "10.0.0.1:1080", RemoteDNS: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.Name != "办公室" || got.Scheme != "socks5h" || !got.RemoteDNS || got.Location != "any" || got.Auth.Set {
		t.Fatalf("Create = %+v", got)
	}
	if !got.CreatedAt.Equal(got.UpdatedAt) || got.CreatedAt.IsZero() {
		t.Errorf("时间 = %v %v", got.CreatedAt, got.UpdatedAt)
	}
	g2, err := f.s.Get(ctx, got.ID)
	if err != nil || g2.ID != got.ID {
		t.Fatalf("Get = %+v, %v", g2, err)
	}
	if _, err := f.s.Create(ctx, input("B")); err != nil {
		t.Fatal(err)
	}
	list, err := f.s.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if empty, _ := newFixture(t).s.List(ctx); empty == nil || len(empty) != 0 {
		t.Errorf("空列表应为非 nil 空切片: %#v", empty)
	}
}

func TestRemoteDNSNormalization(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cases := []struct {
		scheme string
		remote bool
		want   string
		wantR  bool
	}{
		{"socks5", false, "socks5", false},
		{"socks5", true, "socks5h", true},
		{"socks5h", false, "socks5h", true},
		{"http", false, "http", true},
		{"https", true, "https", true},
	}
	for i, c := range cases {
		in := model.ProxyInput{Name: string(rune('a' + i)), Scheme: c.scheme, Address: "h:1", RemoteDNS: c.remote}
		p, err := f.s.Create(ctx, in)
		if err != nil || p.Scheme != c.want || p.RemoteDNS != c.wantR {
			t.Errorf("%+v -> %+v, %v", c, p, err)
		}
	}
}

func TestValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.s.Create(ctx, input("dup")); err != nil {
		t.Fatal(err)
	}
	bad := map[string]model.ProxyInput{
		"name":          {Name: " ", Scheme: "http", Address: "h:1"},
		"scheme":        {Name: "x", Scheme: "ftp", Address: "h:1"},
		"address":       {Name: "x", Scheme: "http", Address: "h"},
		"location":      {Name: "x", Scheme: "http", Address: "h:1", Location: "moon"},
		"auth.username": {Name: "x", Scheme: "http", Address: "h:1", Auth: &model.ProxyAuthInput{Password: "p"}},
		"clear_auth":    {Name: "x", Scheme: "http", Address: "h:1", ClearAuth: true, Auth: &model.ProxyAuthInput{Username: "u"}},
	}
	for field, in := range bad {
		_, err := f.s.Create(ctx, in)
		var fe model.FieldErrors
		if !errors.As(err, &fe) || fe[field] == "" {
			t.Errorf("%s: err = %v", field, err)
		}
	}
	_, err := f.s.Create(ctx, input("dup"))
	var fe model.FieldErrors
	if !errors.As(err, &fe) || fe["name"] != model.FieldDuplicate {
		t.Errorf("重名应得 name=duplicate: %v", err)
	}
	other, _ := f.s.Create(ctx, input("other"))
	_, err = f.s.Update(ctx, other.ID, input("dup"))
	if !errors.As(err, &fe) || fe["name"] != model.FieldDuplicate {
		t.Errorf("更新重名: %v", err)
	}
	// 改成自己的名字不算重名
	if _, err := f.s.Update(ctx, other.ID, input("other")); err != nil {
		t.Errorf("保持原名更新失败: %v", err)
	}
}

func TestAuthEncryptedAndKept(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	in := input("auth")
	in.Auth = &model.ProxyAuthInput{Username: "alice", Password: "s3cret-pw"}
	p, err := f.s.Create(ctx, in)
	if err != nil || !p.Auth.Set {
		t.Fatalf("Create = %+v, %v", p, err)
	}
	var enc string
	if err := f.db.QueryRow(`SELECT auth_enc FROM proxies WHERE id=?`, p.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if enc == "" || containsAny(enc, "alice", "s3cret-pw") {
		t.Fatalf("auth_enc 应为密文: %q", enc)
	}
	// 整行都不应含明文
	rows, _ := f.db.Query(`SELECT id||name||scheme||address||auth_enc FROM proxies`)
	for rows.Next() {
		var all string
		_ = rows.Scan(&all)
		if containsAny(all, "alice", "s3cret-pw") {
			t.Fatal("库中出现明文认证")
		}
	}
	_ = rows.Close()

	resolved, err := f.s.Resolve(ctx, p.ID)
	if err != nil || resolved.URL() != "http://alice:s3cret-pw@127.0.0.1:8080" {
		t.Fatalf("Resolve = %v, %v", resolved.Redacted(), err)
	}

	// PUT 认证缺省 / 空 = 保留
	for _, a := range []*model.ProxyAuthInput{nil, {}} {
		up := input("auth")
		up.Address = "127.0.0.2:9"
		up.Auth = a
		got, err := f.s.Update(ctx, p.ID, up)
		if err != nil || !got.Auth.Set {
			t.Fatalf("Update = %+v, %v", got, err)
		}
		r, _ := f.s.Resolve(ctx, p.ID)
		if r.URL() != "http://alice:s3cret-pw@127.0.0.2:9" {
			t.Fatalf("认证未保留: %s", r.Redacted())
		}
	}
	// 更换
	up := input("auth")
	up.Auth = &model.ProxyAuthInput{Username: "bob", Password: "new"}
	if _, err := f.s.Update(ctx, p.ID, up); err != nil {
		t.Fatal(err)
	}
	r, _ := f.s.Resolve(ctx, p.ID)
	if r.URL() != "http://bob:new@127.0.0.1:8080" {
		t.Errorf("认证未更换: %s", r.Redacted())
	}
	// 清除
	up = input("auth")
	up.ClearAuth = true
	got, err := f.s.Update(ctx, p.ID, up)
	if err != nil || got.Auth.Set {
		t.Fatalf("清除后 = %+v, %v", got, err)
	}
	r, _ = f.s.Resolve(ctx, p.ID)
	if r.URL() != "http://127.0.0.1:8080" {
		t.Errorf("清除后 URL = %s", r.URL())
	}
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		for i := 0; i+len(x) <= len(s); i++ {
			if s[i:i+len(x)] == x {
				return true
			}
		}
	}
	return false
}

func TestResolve(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, id := range []string{"", "direct"} {
		p, err := f.s.Resolve(ctx, id)
		if err != nil || !p.IsDirect() {
			t.Errorf("Resolve(%q) = %v, %v，应为直连", id, p, err)
		}
	}
	if _, err := f.s.Resolve(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestNotFound(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.s.Get(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get: %v", err)
	}
	if _, err := f.s.Update(ctx, "x", input("a")); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update: %v", err)
	}
	if err := f.s.Delete(ctx, "x", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: %v", err)
	}
	if _, err := f.s.Test(ctx, "x", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Test: %v", err)
	}
}

func TestDeleteInUse(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p, _ := f.s.Create(ctx, input("used"))
	f.refs.refs[p.ID] = []model.ProxyReferrer{{ID: "i1", Name: "天气"}, {ID: "i2", Name: "检测"}}

	err := f.s.Delete(ctx, p.ID, false)
	var inUse *InUseError
	if !errors.As(err, &inUse) || len(inUse.Referrers) != 2 || inUse.Referrers[0].Name != "天气" {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.s.Get(ctx, p.ID); err != nil {
		t.Fatal("被引用时不应删除")
	}
	if len(f.refs.resets) != 0 {
		t.Fatal("非 force 不应改引用")
	}

	if err := f.s.Delete(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(f.refs.resets) != 1 || f.refs.resets[0] != p.ID {
		t.Errorf("resets = %v", f.refs.resets)
	}
	if _, err := f.s.Get(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("force 后应已删除: %v", err)
	}
}

func TestDeleteUnused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p, _ := f.s.Create(ctx, input("free"))
	if err := f.s.Delete(ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	if len(f.refs.resets) != 0 {
		t.Error("无引用不应调用改直连")
	}
}

func TestTestViaHTTPProxy(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fake := proxytest.NewHTTP(t)
	in := model.ProxyInput{Name: "h", Scheme: "http", Address: fake.Addr, Auth: &model.ProxyAuthInput{Username: "u", Password: "p"}}
	p, _ := f.s.Create(ctx, in)

	res, err := f.s.Test(ctx, p.ID, "http://target.example.invalid/custom_204")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Status != 204 || res.LatencyMS != 7 || res.URL != "http://target.example.invalid/custom_204" || res.Error != "" {
		t.Fatalf("res = %+v", res)
	}
	seen := fake.Seen()
	if len(seen) != 1 || seen[0].Target != "http://target.example.invalid/custom_204" || seen[0].User != "u" {
		t.Fatalf("seen = %+v", seen)
	}
}

func TestTestViaSocks5h(t *testing.T) {
	f := newFixture(t)
	fake := proxytest.NewSocks5(t, "", "")
	p, _ := f.s.Create(context.Background(), model.ProxyInput{Name: "s", Scheme: "socks5h", Address: fake.Addr})
	res, err := f.s.Test(context.Background(), p.ID, "http://far.example.invalid/generate_204")
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if s := fake.Seen(); len(s) != 1 || !s[0].Domain || s[0].Target != "far.example.invalid:80" {
		t.Fatalf("seen = %+v", s)
	}
}

func TestTestFailureReportsError(t *testing.T) {
	f := newFixture(t)
	// 127.0.0.1:1 通常无监听，连接被拒绝。
	p, _ := f.s.Create(context.Background(), model.ProxyInput{
		Name: "dead", Scheme: "http", Address: "127.0.0.1:1",
		Auth: &model.ProxyAuthInput{Username: "u", Password: "topsecret"},
	})
	res, err := f.s.Test(context.Background(), p.ID, "http://x.example.invalid/")
	if err != nil {
		t.Fatalf("请求失败应体现在结果里而非 error: %v", err)
	}
	if res.OK || res.Error == "" || res.LatencyMS != 0 || res.Status != 0 {
		t.Fatalf("res = %+v", res)
	}
	if containsAny(res.Error, "topsecret") {
		t.Errorf("错误信息泄露密码: %s", res.Error)
	}
}

func TestTestDefaultsAndValidation(t *testing.T) {
	f := newFixture(t)
	p, _ := f.s.Create(context.Background(), model.ProxyInput{Name: "dead", Scheme: "http", Address: "127.0.0.1:1"})
	res, _ := f.s.Test(context.Background(), p.ID, "")
	if res.URL != DefaultTestURL || DefaultTestURL != "https://www.google.com/generate_204" {
		t.Errorf("默认目标 = %q", res.URL)
	}
	for _, bad := range []string{"ftp://x/", "not a url", "http://"} {
		_, err := f.s.Test(context.Background(), p.ID, bad)
		var fe model.FieldErrors
		if !errors.As(err, &fe) || fe["url"] == "" {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}
