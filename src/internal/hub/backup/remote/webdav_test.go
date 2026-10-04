package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/webdav"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

const (
	davUser = "nas-user"
	davPass = "s3cret-Pass"
)

type davServer struct {
	*httptest.Server
	root string
	// denyWrite 为真时对 PUT/MKCOL/MOVE/DELETE 返回 403（x/net/webdav 的权限错误是 404，模拟不出 403）。
	denyWrite bool
}

func newDav(t *testing.T) *davServer {
	t.Helper()
	d := &davServer{root: t.TempDir()}
	h := &webdav.Handler{Prefix: "/dav", FileSystem: webdav.Dir(d.root), LockSystem: webdav.NewMemLS()}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != davUser || p != davPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="dav"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case "PUT", "MKCOL", "MOVE", "DELETE":
			if d.denyWrite {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(d.Close)
	return d
}

func (d *davServer) target(t *testing.T, dir string) *WebDAV {
	t.Helper()
	w, err := NewWebDAV(WebDAVConfig{URL: d.URL + "/dav", Dir: dir, Username: davUser, Password: davPass})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func (d *davServer) names(t *testing.T, rel string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(d.root, rel))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func wantReason(t *testing.T, err error, want Reason) {
	t.Helper()
	var re *Error
	if !errors.As(err, &re) {
		t.Fatalf("不是 *Error: %v", err)
	}
	if re.Reason != want {
		t.Fatalf("原因 = %s，期望 %s（%v）", re.Reason, want, err)
	}
}

func TestWebDAVNewRejectsPublicHTTP(t *testing.T) {
	for _, u := range []string{"http://example.com/dav", "http://8.8.8.8/dav"} {
		if _, err := NewWebDAV(WebDAVConfig{URL: u, Username: "a", Password: "b"}); err == nil {
			t.Errorf("%s 应被拒绝", u)
		}
	}
	for _, u := range []string{"http://192.168.1.5:5005/dav", "http://nas.local/dav", "http://nas/dav", "https://example.com/dav"} {
		if _, err := NewWebDAV(WebDAVConfig{URL: u, Username: "a", Password: "b"}); err != nil {
			t.Errorf("%s 应允许：%v", u, err)
		}
	}
}

func TestWebDAVNewRejectsBadInput(t *testing.T) {
	cases := []WebDAVConfig{
		{URL: "http://u:pw@192.168.1.5/dav"},
		{URL: "ftp://192.168.1.5/dav"},
		{URL: "http://192.168.1.5/dav", Dir: "a/../b"},
		{URL: "http://192.168.1.5/dav?x=1"},
	}
	for _, c := range cases {
		_, err := NewWebDAV(c)
		if err == nil {
			t.Errorf("%+v 应被拒绝", c)
			continue
		}
		if strings.Contains(err.Error(), "pw") {
			t.Errorf("错误含凭据: %v", err)
		}
	}
}

func TestWebDAVEnsureDirCreatesNestedAndIdempotent(t *testing.T) {
	d := newDav(t)
	w := d.target(t, "backups/pimon/daily")
	for i := 0; i < 2; i++ {
		if err := w.EnsureDir(context.Background()); err != nil {
			t.Fatalf("第 %d 次: %v", i+1, err)
		}
	}
	if st, err := os.Stat(filepath.Join(d.root, "backups/pimon/daily")); err != nil || !st.IsDir() {
		t.Fatalf("目录未创建: %v", err)
	}
}

func TestWebDAVPutRenameListRemove(t *testing.T) {
	d := newDav(t)
	w := d.target(t, "bk")
	ctx := context.Background()
	if err := w.EnsureDir(ctx); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 300<<10)
	_, _ = rand.Read(payload)
	if err := w.Put(ctx, ".pimon-upload-a.part", bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if err := w.Rename(ctx, ".pimon-upload-a.part", "a.age"); err != nil {
		t.Fatal(err)
	}
	// 同名已存在时覆盖
	if err := w.Put(ctx, "tmp", strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}
	if err := w.Rename(ctx, "tmp", "a.age"); err != nil {
		t.Fatalf("覆盖改名: %v", err)
	}
	if got := d.names(t, "bk"); len(got) != 1 || got[0] != "a.age" {
		t.Fatalf("目录内容 %v", got)
	}
	b, _ := os.ReadFile(filepath.Join(d.root, "bk/a.age"))
	if string(b) != "new" {
		t.Fatalf("内容未被覆盖: %d 字节", len(b))
	}

	if err := w.Put(ctx, "big.age", bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(d.root, "bk/子 目录"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.root, "bk/中文 文件#1.txt"), []byte("xy"), 0o644); err != nil {
		t.Fatal(err)
	}
	ents, err := w.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Entry{}
	for _, e := range ents {
		got[e.Name] = e
	}
	if len(got) != 4 {
		t.Fatalf("条目 %+v", ents)
	}
	if got["big.age"].Size != int64(len(payload)) || got["big.age"].IsDir {
		t.Errorf("big.age: %+v", got["big.age"])
	}
	if !got["子 目录"].IsDir {
		t.Errorf("子目录: %+v", got["子 目录"])
	}
	if got["中文 文件#1.txt"].Size != 2 {
		t.Errorf("编码文件名: %+v", got)
	}

	if err := w.Remove(ctx, "big.age"); err != nil {
		t.Fatal(err)
	}
	if err := w.Remove(ctx, "big.age"); err != nil {
		t.Fatalf("删除不存在的文件应成功: %v", err)
	}
}

func TestWebDAVListHrefVariants(t *testing.T) {
	body := `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:">
<D:response><D:href>http://h/dav/bk</D:href><D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop></D:propstat></D:response>
<D:response><D:href>/dav/bk/a%20b.age</D:href><D:propstat><D:prop><D:resourcetype/><D:getcontentlength>7</D:getcontentlength></D:prop></D:propstat></D:response>
<D:response><D:href>http://h/dav/bk/sub/</D:href><D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop></D:propstat></D:response>
<D:response><D:href>/dav/bk/sub/deep.age</D:href><D:propstat><D:prop><D:resourcetype/></D:prop></D:propstat></D:response>
</D:multistatus>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	w, err := NewWebDAV(WebDAVConfig{URL: srv.URL + "/dav", Dir: "bk", Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	ents, err := w.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 || ents[0].Name != "a b.age" || ents[0].Size != 7 || ents[1].Name != "sub" || !ents[1].IsDir {
		t.Fatalf("%+v", ents)
	}
}

func TestWebDAVAuthFailure(t *testing.T) {
	d := newDav(t)
	w, err := NewWebDAV(WebDAVConfig{URL: d.URL + "/dav", Dir: "bk", Username: davUser, Password: "wrong-password"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	wantReason(t, w.EnsureDir(ctx), ReasonAuth)
	wantReason(t, w.Put(ctx, "x", strings.NewReader("a")), ReasonAuth)
	_, lerr := w.List(ctx)
	wantReason(t, lerr, ReasonAuth)
	wantReason(t, w.Rename(ctx, "a", "b"), ReasonAuth)
	wantReason(t, w.Remove(ctx, "a"), ReasonAuth)
}

func TestWebDAVNotWritable(t *testing.T) {
	d := newDav(t)
	d.denyWrite = true
	w := d.target(t, "bk")
	ctx := context.Background()
	wantReason(t, w.EnsureDir(ctx), ReasonNotWritable)
	wantReason(t, w.Put(ctx, "x", strings.NewReader("a")), ReasonNotWritable)
	wantReason(t, w.Rename(ctx, "a", "b"), ReasonNotWritable)
	wantReason(t, w.Remove(ctx, "a"), ReasonNotWritable)
}

func TestWebDAVEnsureDirExistingButReadOnlyIsOK(t *testing.T) {
	d := newDav(t)
	if err := os.MkdirAll(filepath.Join(d.root, "bk"), 0o755); err != nil {
		t.Fatal(err)
	}
	d.denyWrite = true
	if err := d.target(t, "bk").EnsureDir(context.Background()); err != nil {
		t.Fatalf("目录已存在时不应报错: %v", err)
	}
}

func TestWebDAVConnectRefusedAndNoCredentialLeak(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close() // 端口立即无人监听
	w, err := NewWebDAV(WebDAVConfig{URL: addr + "/dav", Dir: "bk", Username: davUser, Password: davPass})
	if err != nil {
		t.Fatal(err)
	}
	err = w.EnsureDir(context.Background())
	wantReason(t, err, ReasonConnect)
	assertNoCreds(t, err)
}

func assertNoCreds(t *testing.T, err error) {
	t.Helper()
	s := err.Error()
	var re *Error
	if errors.As(err, &re) {
		s += "|" + re.Message
	}
	for _, bad := range []string{davPass, davUser + ":" + davPass, davUser + ":", "@127.0.0.1"} {
		if strings.Contains(s, bad) {
			t.Errorf("错误文本含 %q: %s", bad, s)
		}
	}
}

func TestWebDAVErrorsDoNotLeakCredentials(t *testing.T) {
	// 服务器在响应里回显口令，也不应进入错误文本；状态码错误只含状态行。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad "+davPass, http.StatusInternalServerError)
	}))
	defer srv.Close()
	w, _ := NewWebDAV(WebDAVConfig{URL: srv.URL + "/dav", Dir: "bk", Username: davUser, Password: davPass})
	ctx := context.Background()
	assertNoCreds(t, w.Put(ctx, "x", strings.NewReader("a")))
	_, err := w.List(ctx)
	assertNoCreds(t, err)
	assertNoCreds(t, w.Rename(ctx, "a", "b"))

	// 连接中途断开（底层是 *url.Error）
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		c, _, _ := hj.Hijack()
		_ = c.Close()
	}))
	defer bad.Close()
	w2, _ := NewWebDAV(WebDAVConfig{URL: bad.URL + "/dav", Dir: "bk", Username: davUser, Password: davPass})
	err = w2.Rename(ctx, "a", "b")
	if err == nil {
		t.Fatal("应失败")
	}
	assertNoCreds(t, err)
	var re *Error
	if errors.As(err, &re) && re.Cause != nil {
		assertNoCreds(t, re.Cause)
	}
}

func TestWebDAVRedactsPasswordFromTransportMessage(t *testing.T) {
	w, _ := NewWebDAV(WebDAVConfig{URL: "http://192.168.1.5/dav", Username: davUser, Password: davPass})
	got := w.redact("dial failed for " + davUser + ":" + davPass + "@host with " + davPass)
	assertNoCreds(t, errors.New(got))
}

func TestWebDAVContextCancelReturnsCtxError(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	w, _ := NewWebDAV(WebDAVConfig{URL: srv.URL + "/dav", Dir: "bk", Username: "u", Password: "p"})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	err := w.Put(ctx, "x", strings.NewReader("a"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 context.Canceled: %v", err)
	}
}

func TestWebDAVRedirectIsIOError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://elsewhere.example/", http.StatusMovedPermanently)
	}))
	defer srv.Close()
	w, _ := NewWebDAV(WebDAVConfig{URL: srv.URL + "/dav", Dir: "bk", Username: "u", Password: "p"})
	wantReason(t, w.Put(context.Background(), "x", strings.NewReader("a")), ReasonIO)
}

// 经 Uploader 完整驱动：临时名上传 → MOVE 正式名 → 清理，只保留 N 份且不碰其他文件。
func TestWebDAVUploaderEndToEnd(t *testing.T) {
	d := newDav(t)
	w := d.target(t, "nas/pimon")
	if err := os.MkdirAll(filepath.Join(d.root, "nas/pimon"), 0o755); err != nil {
		t.Fatal(err)
	}
	keepFiles := []string{"notes.txt", "pimon-backup-20260101-000000-daily.tar.gz"}
	for _, n := range keepFiles {
		if err := os.WriteFile(filepath.Join(d.root, "nas/pimon", n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := []string{"pimon-backup-20260801-020000-daily.tar.gz.age", "pimon-backup-20260802-020000-daily.tar.gz.age", "pimon-backup-20260803-020000-daily.tar.gz.age"}
	for _, n := range old {
		if err := os.WriteFile(filepath.Join(d.root, "nas/pimon", n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(d.root, "nas/pimon", ".pimon-upload-stale.part"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), localNew)
	if err := os.WriteFile(local, bytes.Repeat([]byte("data"), 5000), 0o600); err != nil {
		t.Fatal(err)
	}
	u := &Uploader{Clock: clock.Real{}, Passphrase: testPass, WorkFactor: 10}
	res, err := u.Upload(context.Background(), w, local, localNew, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.CleanupErr != nil {
		t.Fatal(res.CleanupErr)
	}
	got := d.names(t, "nas/pimon")
	want := []string{"notes.txt", "pimon-backup-20260101-000000-daily.tar.gz", "pimon-backup-20260803-020000-daily.tar.gz.age", localNew + ".age"}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("目录 %v，期望 %v", got, want)
	}
	if st, _ := os.Stat(filepath.Join(d.root, "nas/pimon", localNew+".age")); st == nil || st.Size() != res.Bytes {
		t.Fatalf("远端大小与结果不符: %+v vs %d", st, res.Bytes)
	}
}

func TestWebDAVUploaderAuthFailureReason(t *testing.T) {
	d := newDav(t)
	w, _ := NewWebDAV(WebDAVConfig{URL: d.URL + "/dav", Dir: "bk", Username: davUser, Password: "nope"})
	local := filepath.Join(t.TempDir(), localNew)
	_ = os.WriteFile(local, []byte("d"), 0o600)
	u := &Uploader{Clock: clock.Real{}, Passphrase: testPass, WorkFactor: 10}
	_, err := u.Upload(context.Background(), w, local, localNew, 2)
	wantReason(t, err, ReasonAuth)
}
