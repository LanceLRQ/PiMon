package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

const (
	testPass = "unit-test-passphrase"
	localOld = "pimon-backup-20260901-020000-daily.tar.gz"
	localNew = "pimon-backup-20260930-020000-daily.tar.gz"
)

// memTarget 是内存中的 Target，可注入各阶段的失败。
type memTarget struct {
	mu        sync.Mutex
	files     map[string][]byte
	dirs      map[string]bool
	putSeen   []string // Put 开始时的文件名
	ensureErr error
	putErr    error
	renameErr error
	listErr   error
	removeErr map[string]error
	putHook   func(ctx context.Context, name string, r io.Reader) error
}

func newMem() *memTarget {
	return &memTarget{files: map[string][]byte{}, dirs: map[string]bool{}, removeErr: map[string]error{}}
}

func (m *memTarget) EnsureDir(context.Context) error { return m.ensureErr }

func (m *memTarget) Put(ctx context.Context, name string, r io.Reader) error {
	m.mu.Lock()
	m.putSeen = append(m.putSeen, name)
	hook := m.putHook
	m.mu.Unlock()
	if hook != nil {
		return hook(ctx, name, r)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if m.putErr != nil {
		m.mu.Lock()
		m.files[name] = data[:len(data)/2] // 模拟留下半个文件
		m.mu.Unlock()
		return m.putErr
	}
	m.mu.Lock()
	m.files[name] = data
	m.mu.Unlock()
	return nil
}

func (m *memTarget) Rename(_ context.Context, from, to string) error {
	if m.renameErr != nil {
		return m.renameErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[to] = m.files[from]
	delete(m.files, from)
	return nil
}

func (m *memTarget) List(context.Context) ([]Entry, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Entry
	for n, d := range m.files {
		out = append(out, Entry{Name: n, Size: int64(len(d))})
	}
	for n := range m.dirs {
		out = append(out, Entry{Name: n, IsDir: true})
	}
	return out, nil
}

func (m *memTarget) Remove(_ context.Context, name string) error {
	if err := m.removeErr[name]; err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, name)
	return nil
}

func (m *memTarget) Close() error { return nil }

func (m *memTarget) names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for n := range m.files {
		out = append(out, n)
	}
	for n := range m.dirs {
		out = append(out, n+"/")
	}
	sort.Strings(out)
	return out
}

func writeLocal(t *testing.T, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	_, _ = rand.Read(data)
	p := filepath.Join(t.TempDir(), localNew)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, data
}

func newUploader(clk clock.Clock) *Uploader {
	return &Uploader{Clock: clk, Passphrase: testPass, WorkFactor: 10}
}

func TestUploadRoundTripAndTempName(t *testing.T) {
	path, plain := writeLocal(t, 200_000)
	m := newMem()
	res, err := newUploader(clock.Real{}).Upload(context.Background(), m, path, localNew, 5)
	if err != nil {
		t.Fatal(err)
	}
	want := localNew + ".age"
	if res.RemoteName != want {
		t.Fatalf("RemoteName = %q", res.RemoteName)
	}
	if got := m.names(); len(got) != 1 || got[0] != want {
		t.Fatalf("上传后只应有正式名，得到 %v", got)
	}
	if len(m.putSeen) != 1 || m.putSeen[0] != ".pimon-upload-"+want+".part" {
		t.Fatalf("应先用临时名上传，得到 %v", m.putSeen)
	}
	cipher := m.files[want]
	if res.Bytes != int64(len(cipher)) {
		t.Fatalf("Bytes = %d，密文 %d", res.Bytes, len(cipher))
	}
	dec, err := backup.DecryptReader(bytes.NewReader(cipher), testPass)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(dec)
	if !bytes.Equal(got, plain) {
		t.Fatal("远端密文解密后与本地备份不一致")
	}
}

func TestUploadOverwritesSameName(t *testing.T) {
	path, _ := writeLocal(t, 1000)
	m := newMem()
	u := newUploader(clock.Real{})
	for range 2 {
		if _, err := u.Upload(context.Background(), m, path, localNew, 5); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.names(); len(got) != 1 {
		t.Fatalf("重复上传同名应覆盖，得到 %v", got)
	}
}

func TestUploadRetentionOnlyTouchesOwnFiles(t *testing.T) {
	path, _ := writeLocal(t, 1000)
	m := newMem()
	for _, n := range []string{
		"pimon-backup-20260801-020000-daily.tar.gz.age",
		"pimon-backup-20260815-020000-manual.tar.gz.age",
		"pimon-backup-20260901-020000-pre-upgrade.tar.gz.age",
		"pimon-backup-20260920-020000-daily.tar.gz.age",
		// 以下不属于本项目的加密备份，必须原样保留
		"notes.txt",
		"pimon-backup-20260101-020000-daily.tar.gz", // 未加密的本地命名
		"pimon-backup-garbage.tar.gz.age",
		"photo.jpg.age",
	} {
		m.files[n] = []byte("x")
	}
	m.dirs["pimon-backup-20250101-020000-daily.tar.gz.age"] = true // 同名目录不是文件
	m.files[".pimon-upload-pimon-backup-20260101-020000-daily.tar.gz.age.part"] = []byte("half")
	if _, err := newUploader(clock.Real{}).Upload(context.Background(), m, path, localNew, 3); err != nil {
		t.Fatal(err)
	}
	got := m.names()
	expect := []string{
		"notes.txt",
		"photo.jpg.age",
		"pimon-backup-20250101-020000-daily.tar.gz.age/",
		"pimon-backup-20260101-020000-daily.tar.gz",
		"pimon-backup-20260901-020000-pre-upgrade.tar.gz.age",
		"pimon-backup-20260920-020000-daily.tar.gz.age",
		"pimon-backup-20260930-020000-daily.tar.gz.age",
		"pimon-backup-garbage.tar.gz.age",
	}
	if strings.Join(got, "|") != strings.Join(expect, "|") {
		t.Fatalf("清理结果不符\n得到 %v\n期望 %v", got, expect)
	}
}

func TestUploadKeepOne(t *testing.T) {
	path, _ := writeLocal(t, 100)
	m := newMem()
	m.files["pimon-backup-20260901-020000-daily.tar.gz.age"] = []byte("x")
	if _, err := newUploader(clock.Real{}).Upload(context.Background(), m, path, localNew, 1); err != nil {
		t.Fatal(err)
	}
	if got := m.names(); len(got) != 1 || got[0] != localNew+".age" {
		t.Fatalf("keep=1 应只剩新文件，得到 %v", got)
	}
}

func TestUploadRejectsBadArguments(t *testing.T) {
	path, _ := writeLocal(t, 10)
	u := newUploader(clock.Real{})
	m := newMem()
	if _, err := u.Upload(context.Background(), m, path, "evil.tar.gz", 3); err == nil {
		t.Fatal("非备份命名应拒绝")
	}
	if _, err := u.Upload(context.Background(), m, path, localNew, 0); err == nil {
		t.Fatal("keep=0 应拒绝")
	}
	if _, err := u.Upload(context.Background(), m, filepath.Join(t.TempDir(), "none"), localNew, 3); err == nil {
		t.Fatal("本地文件不存在应报错")
	}
	empty := &Uploader{Clock: clock.Real{}, WorkFactor: 10}
	if _, err := empty.Upload(context.Background(), m, path, localNew, 3); err == nil {
		t.Fatal("空口令应拒绝")
	}
	if len(m.names()) != 0 || len(m.putSeen) != 0 {
		t.Fatal("参数错误时不应触碰远端")
	}
}

func TestUploadPutFailureCleansTempAndKeepsReason(t *testing.T) {
	path, _ := writeLocal(t, 5000)
	m := newMem()
	m.files["pimon-backup-20260901-020000-daily.tar.gz.age"] = []byte("x")
	m.putErr = NewError(ReasonAuth, "用户名或密码错误", nil)
	_, err := newUploader(clock.Real{}).Upload(context.Background(), m, path, localNew, 3)
	var re *Error
	if !errors.As(err, &re) || re.Reason != ReasonAuth {
		t.Fatalf("err = %v，期望 auth", err)
	}
	if got := m.names(); len(got) != 1 || got[0] != "pimon-backup-20260901-020000-daily.tar.gz.age" {
		t.Fatalf("失败后应删除临时文件且不动旧备份，得到 %v", got)
	}
}

func TestUploadRenameFailureCleansTemp(t *testing.T) {
	path, _ := writeLocal(t, 5000)
	m := newMem()
	m.renameErr = NewError(ReasonNotWritable, "无权改名", nil)
	_, err := newUploader(clock.Real{}).Upload(context.Background(), m, path, localNew, 3)
	var re *Error
	if !errors.As(err, &re) || re.Reason != ReasonNotWritable {
		t.Fatalf("err = %v", err)
	}
	if len(m.names()) != 0 {
		t.Fatalf("改名失败后不应留下文件，得到 %v", m.names())
	}
}

func TestUploadEnsureDirAndListErrors(t *testing.T) {
	path, _ := writeLocal(t, 100)
	m := newMem()
	m.ensureErr = NewError(ReasonNotWritable, "无法创建目录", nil)
	_, err := newUploader(clock.Real{}).Upload(context.Background(), m, path, localNew, 3)
	var re *Error
	if !errors.As(err, &re) || re.Reason != ReasonNotWritable {
		t.Fatalf("err = %v", err)
	}
	m = newMem()
	m.listErr = &net.OpError{Op: "read", Err: errors.New("reset")}
	_, err = newUploader(clock.Real{}).Upload(context.Background(), m, path, localNew, 3)
	if !errors.As(err, &re) || re.Reason != ReasonConnect {
		t.Fatalf("网络错误应归 connect，得到 %v", err)
	}
}

func TestUploadCleanupErrorDoesNotFailUpload(t *testing.T) {
	path, _ := writeLocal(t, 100)
	m := newMem()
	old := "pimon-backup-20260901-020000-daily.tar.gz.age"
	m.files[old] = []byte("x")
	m.removeErr[old] = NewError(ReasonNotWritable, "无权删除", nil)
	res, err := newUploader(clock.Real{}).Upload(context.Background(), m, path, localNew, 1)
	if err != nil {
		t.Fatalf("清理失败不应使上传失败: %v", err)
	}
	var re *Error
	if !errors.As(res.CleanupErr, &re) || re.Reason != ReasonNotWritable {
		t.Fatalf("CleanupErr = %v", res.CleanupErr)
	}
}

func TestUploadCallerCancel(t *testing.T) {
	path, _ := writeLocal(t, 100)
	m := newMem()
	m.putHook = func(ctx context.Context, _ string, _ io.Reader) error {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := newUploader(clock.Real{}).Upload(ctx, m, path, localNew, 3)
		errc <- err
	}()
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v，期望 context.Canceled", err)
	}
}

func waitWaiters(t *testing.T, fk *clock.Fake, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for fk.Waiters() != n {
		if time.Now().After(deadline) {
			t.Fatalf("等待者数量 %d，期望 %d", fk.Waiters(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestUploadStallAborts(t *testing.T) {
	path, _ := writeLocal(t, 100_000)
	fk := clock.NewFake(time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC))
	m := newMem()
	m.putHook = func(ctx context.Context, _ string, r io.Reader) error {
		buf := make([]byte, 100)
		if _, err := r.Read(buf); err != nil {
			return err
		}
		<-ctx.Done() // 之后不再读取，模拟网络卡死
		return ctx.Err()
	}
	errc := make(chan error, 1)
	go func() {
		_, err := newUploader(fk).Upload(context.Background(), m, path, localNew, 3)
		errc <- err
	}()
	waitWaiters(t, fk, 1)
	fk.Advance(StallTimeout)
	err := <-errc
	var re *Error
	if !errors.As(err, &re) || re.Reason != ReasonIO || !strings.Contains(re.Message, "没有进展") {
		t.Fatalf("err = %v，期望 io 无进展", err)
	}
	if len(m.names()) != 0 {
		t.Fatalf("中止后不应留下文件: %v", m.names())
	}
}

func TestUploadProgressResetsStallTimer(t *testing.T) {
	path, _ := writeLocal(t, 300_000)
	fk := clock.NewFake(time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC))
	m := newMem()
	step := make(chan struct{})
	ack := make(chan struct{})
	m.putHook = func(ctx context.Context, _ string, r io.Reader) error {
		buf := make([]byte, 100)
		for range 2 { // 每个阶段读一次，证明读取会重置计时
			if _, err := r.Read(buf); err != nil {
				return err
			}
			ack <- struct{}{}
			select {
			case <-step:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		_, err := io.Copy(io.Discard, r)
		return err
	}
	errc := make(chan error, 1)
	go func() {
		_, err := newUploader(fk).Upload(context.Background(), m, path, localNew, 3)
		errc <- err
	}()
	<-ack
	waitWaiters(t, fk, 1)
	fk.Advance(4 * time.Minute) // 未到 5 分钟，等待者重新排队
	waitWaiters(t, fk, 1)
	step <- struct{}{}
	<-ack // 第二次读取，进展时间更新到 T+4m
	fk.Advance(4 * time.Minute)
	waitWaiters(t, fk, 1) // 距上次进展仅 4 分钟，仍未取消
	step <- struct{}{}
	if err := <-errc; err != nil {
		t.Fatalf("有进展时不应被中止: %v", err)
	}
}

func TestClassify(t *testing.T) {
	if Classify(nil) != nil {
		t.Fatal("nil 应返回 nil")
	}
	own := NewError(ReasonAuth, "x", nil)
	if Classify(own) != own {
		t.Fatal("已分类错误应原样返回")
	}
	wrapped := Classify(errors.Join(errors.New("ctx"), own))
	if wrapped.Reason != ReasonAuth {
		t.Fatal("被包装的分类错误应识别")
	}
	if Classify(&net.DNSError{Err: "no such host", Name: "nas"}).Reason != ReasonConnect {
		t.Fatal("网络错误归 connect")
	}
	if Classify(os.ErrPermission).Reason != ReasonNotWritable {
		t.Fatal("权限错误归 not_writable")
	}
	if Classify(errors.New("boom")).Reason != ReasonIO {
		t.Fatal("其余归 io")
	}
}

func TestErrorMessageScrubsCredentials(t *testing.T) {
	ue := &url.Error{Op: "Put", URL: "https://admin:s3cret@nas.local/dav/x", Err: errors.New("EOF")}
	e := Classify(ue)
	if strings.Contains(e.Message, "s3cret") || strings.Contains(e.Error(), "s3cret") {
		t.Fatalf("信息泄露了密码: %q", e.Error())
	}
	if !strings.Contains(e.Message, "https://***@nas.local") {
		t.Fatalf("应保留主机并遮盖凭据: %q", e.Message)
	}
}
