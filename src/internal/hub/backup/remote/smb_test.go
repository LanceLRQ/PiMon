package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hirochachacha/go-smb2"
)

const (
	smbUser = "nas-user"
	smbPass = "Sm8-s3cret"
)

// fakeFile 是内存文件；写入受 gate 控制以模拟阻塞。
type fakeFile struct {
	sh   *fakeShare
	name string
	buf  bytes.Buffer
}

func (f *fakeFile) Write(b []byte) (int, error) {
	if f.sh.writeErr != nil {
		return 0, f.sh.writeErr
	}
	if f.sh.blockWrite != nil {
		<-f.sh.blockWrite
		return 0, &smb2.TransportError{Err: net.ErrClosed}
	}
	return f.buf.Write(b)
}

func (f *fakeFile) Close() error {
	f.sh.mu.Lock()
	defer f.sh.mu.Unlock()
	f.sh.files[f.name] = f.buf.Bytes()
	return nil
}

type fakeInfo struct {
	name string
	size int64
	dir  bool
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return i.size }
func (i fakeInfo) Mode() fs.FileMode  { return 0 }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return i.dir }
func (i fakeInfo) Sys() any           { return nil }

type fakeShare struct {
	mu         sync.Mutex
	files      map[string][]byte
	dirs       map[string]bool
	ops        []string
	opErr      map[string]error // 按操作名注入错误
	writeErr   error
	blockWrite chan struct{} // 非 nil 时 Write 阻塞直到被关闭
	aborted    chan struct{}
	abortOnce  sync.Once
	closed     bool
}

func newFakeShare() *fakeShare {
	return &fakeShare{files: map[string][]byte{}, dirs: map[string]bool{}, opErr: map[string]error{}, aborted: make(chan struct{})}
}

func (f *fakeShare) rec(op string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, op)
	return f.opErr[op]
}

func (f *fakeShare) MkdirAll(_ context.Context, dir string) error {
	if err := f.rec("mkdirall " + dir); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(dir, "/")
	for i := range parts {
		f.dirs[strings.Join(parts[:i+1], "/")] = true
	}
	return nil
}

func (f *fakeShare) Create(_ context.Context, name string) (io.WriteCloser, error) {
	if err := f.rec("create " + name); err != nil {
		return nil, err
	}
	return &fakeFile{sh: f, name: name}, nil
}

func (f *fakeShare) Rename(_ context.Context, from, to string) error {
	if err := f.rec("rename " + from + " " + to); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[to]; ok {
		return &smb2.ResponseError{Code: 0xC0000035} // 目标已存在时 go-smb2 的改名会失败
	}
	b, ok := f.files[from]
	if !ok {
		return &smb2.ResponseError{Code: statusObjectNameNotFound}
	}
	delete(f.files, from)
	f.files[to] = b
	return nil
}

func (f *fakeShare) Remove(_ context.Context, name string) error {
	if err := f.rec("remove " + name); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[name]; !ok {
		return &os.PathError{Op: "remove", Path: name, Err: &smb2.ResponseError{Code: statusObjectNameNotFound}}
	}
	delete(f.files, name)
	return nil
}

func (f *fakeShare) ReadDir(_ context.Context, dir string) ([]fs.FileInfo, error) {
	if err := f.rec("readdir " + dir); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if dir != "" && !f.dirs[dir] {
		return nil, &smb2.ResponseError{Code: statusObjectNameNotFound}
	}
	var out []fs.FileInfo
	prefix := dir + "/"
	if dir == "" {
		prefix = ""
	}
	for p, b := range f.files {
		if strings.HasPrefix(p, prefix) && !strings.Contains(strings.TrimPrefix(p, prefix), "/") {
			out = append(out, fakeInfo{name: strings.TrimPrefix(p, prefix), size: int64(len(b))})
		}
	}
	out = append(out, fakeInfo{name: "子目录", dir: true}, fakeInfo{name: "."})
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (f *fakeShare) Abort() { f.abortOnce.Do(func() { close(f.aborted) }) }
func (f *fakeShare) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func newSMBTarget(t *testing.T, dir string, sh *fakeShare) (*SMB, *int) {
	t.Helper()
	dials := 0
	tg, err := newSMB(SMBConfig{Host: "nas.lan", Share: "backup", Dir: dir, Username: smbUser, Password: smbPass},
		func(context.Context, SMBConfig) (smbShare, error) { dials++; return sh, nil })
	if err != nil {
		t.Fatal(err)
	}
	return tg, &dials
}

func reasonOf(t *testing.T, err error) Reason {
	t.Helper()
	var re *Error
	if !errors.As(err, &re) {
		t.Fatalf("不是 *Error: %v", err)
	}
	return re.Reason
}

func TestSMBEnsureDir逐级(t *testing.T) {
	sh := newFakeShare()
	tg, _ := newSMBTarget(t, "/backups//pimon/", sh)
	if err := tg.EnsureDir(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !sh.dirs["backups"] || !sh.dirs["backups/pimon"] {
		t.Fatalf("目录未创建: %v", sh.dirs)
	}
	if err := tg.EnsureDir(context.Background()); err != nil {
		t.Fatal("已存在不应出错", err)
	}
}

func TestSMBPutRename覆盖已存在目标(t *testing.T) {
	sh := newFakeShare()
	tg, _ := newSMBTarget(t, "b", sh)
	ctx := context.Background()
	_ = tg.EnsureDir(ctx)
	for _, content := range []string{"旧内容", "新内容"} {
		if err := tg.Put(ctx, ".pimon-upload-x.part", strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
		if err := tg.Rename(ctx, ".pimon-upload-x.part", "pimon-backup-x"); err != nil {
			t.Fatal(err)
		}
	}
	if string(sh.files["b/pimon-backup-x"]) != "新内容" {
		t.Fatalf("目标内容不对: %q", sh.files["b/pimon-backup-x"])
	}
	if _, ok := sh.files["b/.pimon-upload-x.part"]; ok {
		t.Fatal("临时文件应已改名")
	}
}

func TestSMBListRemove(t *testing.T) {
	sh := newFakeShare()
	tg, _ := newSMBTarget(t, "b", sh)
	ctx := context.Background()
	_ = tg.EnsureDir(ctx)
	_ = tg.Put(ctx, "a.age", strings.NewReader("12345"))
	es, err := tg.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		if e.IsDir {
			continue
		}
		names = append(names, fmt.Sprintf("%s:%d", e.Name, e.Size))
	}
	if len(names) != 1 || names[0] != "a.age:5" {
		t.Fatalf("文件列表不对: %v（全部 %+v）", names, es)
	}
	for _, e := range es {
		if e.Name == "." || e.Name == ".." {
			t.Fatal("不应返回点号项")
		}
	}
	if err := tg.Remove(ctx, "a.age"); err != nil {
		t.Fatal(err)
	}
	if err := tg.Remove(ctx, "a.age"); err != nil {
		t.Fatal("不存在应视为成功", err)
	}
	if err := tg.Remove(ctx, "../x"); err == nil {
		t.Fatal("路径逃逸应被拒绝")
	}
}

func TestSMBListMissingDir(t *testing.T) {
	tg, _ := newSMBTarget(t, "nope", newFakeShare())
	_, err := tg.List(context.Background())
	if reasonOf(t, err) != ReasonIO {
		t.Fatalf("got %v", err)
	}
}

func TestSMBErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Reason
	}{
		{"登录失败", &smb2.ResponseError{Code: statusLogonFailure}, ReasonAuth},
		{"密码错误", &smb2.ResponseError{Code: statusWrongPassword}, ReasonAuth},
		{"账户禁用", &smb2.ResponseError{Code: statusAccountDisabled}, ReasonAuth},
		{"账户锁定", &smb2.ResponseError{Code: statusAccountLockedOut}, ReasonAuth},
		{"拒绝访问", &smb2.ResponseError{Code: statusAccessDenied}, ReasonNotWritable},
		{"包装的拒绝访问", &os.PathError{Op: "create", Path: "x", Err: &smb2.ResponseError{Code: statusAccessDenied}}, ReasonNotWritable},
		{"网络拒绝访问", &smb2.ResponseError{Code: statusNetworkAccessDenied}, ReasonNotWritable},
		{"共享不存在", &smb2.ResponseError{Code: statusBadNetworkName}, ReasonIO},
		{"磁盘满", &smb2.ResponseError{Code: statusDiskFull}, ReasonIO},
		{"未知状态", &smb2.ResponseError{Code: 0xC0000001}, ReasonIO},
		{"传输错误", &smb2.TransportError{Err: io.ErrUnexpectedEOF}, ReasonConnect},
		{"拨号失败", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}, ReasonConnect},
		{"连接被关闭", net.ErrClosed, ReasonConnect},
		{"其他", errors.New("boom"), ReasonIO},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sh := newFakeShare()
			sh.opErr["readdir b"] = c.err
			sh.dirs["b"] = true
			tg, _ := newSMBTarget(t, "b", sh)
			_, err := tg.List(context.Background())
			if got := reasonOf(t, err); got != c.want {
				t.Fatalf("got %s want %s (%v)", got, c.want, err)
			}
		})
	}
}

func TestSMBDial失败归类(t *testing.T) {
	for _, c := range []struct {
		err  error
		want Reason
	}{
		{&smb2.ResponseError{Code: statusLogonFailure}, ReasonAuth},
		{&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}, ReasonConnect},
	} {
		tg, err := newSMB(SMBConfig{Host: "h", Share: "s", Username: smbUser, Password: smbPass},
			func(context.Context, SMBConfig) (smbShare, error) { return nil, c.err })
		if err != nil {
			t.Fatal(err)
		}
		if got := reasonOf(t, tg.EnsureDir(context.Background())); got != c.want {
			t.Fatalf("got %s want %s", got, c.want)
		}
	}
}

func TestSMBEnsureDir拒绝访问为不可写(t *testing.T) {
	sh := newFakeShare()
	sh.opErr["mkdirall b"] = &os.PathError{Op: "mkdir", Path: "b", Err: &smb2.ResponseError{Code: statusAccessDenied}}
	tg, _ := newSMBTarget(t, "b", sh)
	if got := reasonOf(t, tg.EnsureDir(context.Background())); got != ReasonNotWritable {
		t.Fatalf("got %s", got)
	}
}

func TestSMBErrorNoPassword(t *testing.T) {
	leaky := []error{
		fmt.Errorf("dial smb://%s:%s@nas.lan failed", smbUser, smbPass),
		fmt.Errorf("auth for %s with %s failed", smbUser, smbPass),
		&net.OpError{Op: "dial", Net: "tcp", Err: fmt.Errorf("bad %s:%s", smbUser, smbPass)},
	}
	for _, e := range leaky {
		sh := newFakeShare()
		sh.dirs["b"] = true
		sh.opErr["readdir b"] = e
		tg, _ := newSMBTarget(t, "b", sh)
		_, err := tg.List(context.Background())
		if err == nil || strings.Contains(err.Error(), smbPass) || strings.Contains(err.Error(), smbUser) {
			t.Fatalf("错误文本泄露凭据: %v", err)
		}
	}
	// 拨号失败路径也一样。
	tg, _ := newSMB(SMBConfig{Host: "h", Share: "s", Username: smbUser, Password: smbPass},
		func(context.Context, SMBConfig) (smbShare, error) { return nil, leaky[0] })
	if err := tg.EnsureDir(context.Background()); err == nil || strings.Contains(err.Error(), smbPass) {
		t.Fatalf("拨号错误泄露口令: %v", err)
	}
}

func TestSMBPut取消时切断连接并可重连(t *testing.T) {
	sh := newFakeShare()
	sh.blockWrite = make(chan struct{})
	tg, dials := newSMBTarget(t, "b", sh)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tg.Put(ctx, "x.part", strings.NewReader("data")) }()
	// 等 Put 进入写入。
	for i := 0; i < 1000; i++ {
		sh.mu.Lock()
		n := len(sh.ops)
		sh.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-sh.aborted:
		close(sh.blockWrite) // 模拟连接关闭后阻塞的写返回
	case <-time.After(5 * time.Second):
		t.Fatal("取消后未切断连接")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Put 未返回")
	}
	// 连接已丢弃，下一次操作重新拨号。
	_, _ = tg.List(context.Background()) // 目录不存在导致失败，这里只关心重新拨号
	if *dials != 2 {
		t.Fatalf("拨号次数 %d，期望 2", *dials)
	}
}

func TestSMBPut写入失败归类(t *testing.T) {
	sh := newFakeShare()
	sh.writeErr = &smb2.ResponseError{Code: statusDiskFull}
	tg, _ := newSMBTarget(t, "b", sh)
	err := tg.Put(context.Background(), "x", strings.NewReader("d"))
	if reasonOf(t, err) != ReasonIO {
		t.Fatalf("got %v", err)
	}
}

func TestSMB连接类错误丢弃连接(t *testing.T) {
	sh := newFakeShare()
	sh.dirs["b"] = true
	sh.opErr["readdir b"] = &smb2.TransportError{Err: io.EOF}
	tg, dials := newSMBTarget(t, "b", sh)
	_, _ = tg.List(context.Background())
	_, _ = tg.List(context.Background())
	if *dials != 2 {
		t.Fatalf("连接类错误后应重连，拨号 %d 次", *dials)
	}
}

func TestSMBConfigValidation(t *testing.T) {
	bad := []SMBConfig{
		{Share: "s", Username: "u"},
		{Host: "h", Username: "u"},
		{Host: "h", Share: "a/b", Username: "u"},
		{Host: "h", Share: "s"},
		{Host: "h", Share: "s", Username: "u", Dir: "a/../b"},
		{Host: "h", Share: "s", Username: "u", Port: 70000},
	}
	for i, c := range bad {
		if _, err := NewSMB(c); err == nil {
			t.Fatalf("用例 %d 应被拒绝", i)
		}
	}
	tg, err := NewSMB(SMBConfig{Host: "h", Share: "s", Username: "u"})
	if err != nil || tg.cfg.Port != 445 {
		t.Fatalf("默认端口: %v %v", err, tg)
	}
}

func TestSMBDial超时与不可达(t *testing.T) {
	// 关闭的端口：拨号立即失败，归 connect，不含口令。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	tg, _ := NewSMB(SMBConfig{Host: "127.0.0.1", Port: port, Share: "s", Username: smbUser, Password: smbPass})
	err = tg.EnsureDir(context.Background())
	if reasonOf(t, err) != ReasonConnect || strings.Contains(err.Error(), smbPass) {
		t.Fatalf("got %v", err)
	}
}

func TestSMB握手受ctx约束(t *testing.T) {
	// 只接受连接、不回应：握手必须随 ctx 取消返回，而不是阻塞。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer func() { _ = c.Close() }()
			_, _ = io.Copy(io.Discard, c)
		}
	}()
	tg, _ := NewSMB(SMBConfig{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Share: "s", Username: smbUser, Password: smbPass})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = tg.EnsureDir(ctx)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("握手应随 ctx 终止，耗时 %v，err=%v", time.Since(start), err)
	}
}
