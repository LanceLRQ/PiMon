package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// fakeLocal 是只含一份（或零份）本地备份的 LocalBackups。
type fakeLocal struct {
	dir  string
	list []model.BackupInfo
}

func (f *fakeLocal) List() ([]model.BackupInfo, error) { return f.list, nil }
func (f *fakeLocal) Path(name string) (string, error) { return filepath.Join(f.dir, name), nil }

type svcFixture struct {
	svc     *Service
	repo    *Repo
	mem     *memTarget
	local   *fakeLocal
	targets atomic.Int64 // NewTarget 被调用的次数
	cancel  context.CancelFunc
	done    <-chan struct{}
}

func newSvc(t *testing.T) *svcFixture {
	t.Helper()
	repo, _, clk := newRepo(t)
	path, _ := writeLocal(t, 4000)
	f := &svcFixture{repo: repo, mem: newMem(),
		local: &fakeLocal{dir: filepath.Dir(path), list: []model.BackupInfo{{Name: localNew, Reason: "daily"}}}}
	f.svc = NewService(ServiceConfig{
		Repo: repo, Local: f.local, Clock: clk, WorkFactor: 10,
		NewTarget: func(Config) (Target, error) { f.targets.Add(1); return f.mem, nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel, f.done = cancel, f.svc.Start(ctx)
	t.Cleanup(func() {
		cancel()
		<-f.done
	})
	return f
}

func (f *svcFixture) enable(t *testing.T) {
	t.Helper()
	if _, err := f.repo.Put(context.Background(), smbInput()); err != nil {
		t.Fatal(err)
	}
}

func (f *svcFixture) uploads(t *testing.T) []model.BackupUpload {
	t.Helper()
	list, err := f.repo.Uploads(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// captureLog 把默认 slog 输出接到缓冲区，测试结束时恢复。
func captureLog(t *testing.T) func() string {
	t.Helper()
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}), nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

func assertNoSecrets(t *testing.T, where, s string) {
	t.Helper()
	in := smbInput()
	for _, sec := range []string{in.SMBPassword, in.Passphrase} {
		if strings.Contains(s, sec) {
			t.Fatalf("%s 含有密钥 %q：%s", where, sec, s)
		}
	}
}

func TestAfterDailyUploadsWhenEnabled(t *testing.T) {
	f := newSvc(t)
	f.enable(t)
	logs := captureLog(t)
	f.svc.AfterDaily(context.Background(), f.local.list[0])
	if got := f.mem.names(); len(got) != 1 || got[0] != localNew+".age" {
		t.Fatalf("远端文件 = %v", got)
	}
	ups := f.uploads(t)
	if len(ups) != 1 || !ups[0].Success || ups[0].Name != localNew+".age" || ups[0].TargetType != "smb" || ups[0].Size <= 0 || ups[0].Reason != "" {
		t.Fatalf("记录 = %+v", ups)
	}
	if !strings.Contains(logs(), "远端备份已上传") {
		t.Fatalf("应写成功日志：%s", logs())
	}
}

func TestAfterDailySkipsWhenDisabled(t *testing.T) {
	f := newSvc(t)
	f.svc.AfterDaily(context.Background(), f.local.list[0])
	if f.targets.Load() != 0 || len(f.uploads(t)) != 0 {
		t.Fatalf("未启用不应上传也不应记录：targets=%d", f.targets.Load())
	}
}

func TestAfterDailyFailureRecordedAndLoggedWithoutSecrets(t *testing.T) {
	f := newSvc(t)
	f.enable(t)
	logs := captureLog(t)
	f.mem.putErr = NewError(ReasonAuth, "用户名或密码错误（smb://u:smb-secret-pw@nas.lan）", nil)
	f.svc.AfterDaily(context.Background(), f.local.list[0])
	ups := f.uploads(t)
	if len(ups) != 1 || ups[0].Success || !strings.HasPrefix(ups[0].Reason, "auth：") || ups[0].Size <= 0 {
		t.Fatalf("记录 = %+v", ups)
	}
	assertNoSecrets(t, "记录", ups[0].Reason)
	if !strings.Contains(logs(), "远端备份上传失败") {
		t.Fatalf("应写失败日志：%s", logs())
	}
	assertNoSecrets(t, "日志", logs())
	// 失败不重试：只调用过一次目标。
	if f.targets.Load() != 1 {
		t.Fatalf("失败后不应重试，NewTarget 调用 %d 次", f.targets.Load())
	}
}

func TestCleanupFailureRecordedAsSuccessWithNote(t *testing.T) {
	f := newSvc(t)
	in := smbInput()
	in.Keep = 1
	if _, err := f.repo.Put(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	old := localOld + ".age"
	f.mem.files[old] = []byte("x")
	f.mem.removeErr[old] = NewError(ReasonNotWritable, "拒绝删除", nil)
	logs := captureLog(t)
	u, err := f.svc.UploadNow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !u.Success || !strings.Contains(u.Reason, "清理远端过期备份失败") || !strings.Contains(u.Reason, "not_writable") {
		t.Fatalf("记录 = %+v", u)
	}
	if !strings.Contains(logs(), "清理远端过期备份失败") {
		t.Fatalf("日志应注明清理失败：%s", logs())
	}
}

func TestUploadNowErrors(t *testing.T) {
	f := newSvc(t)
	if _, err := f.svc.UploadNow(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("未启用：%v", err)
	}
	f.enable(t)
	f.local.list = nil
	if _, err := f.svc.UploadNow(context.Background()); !errors.Is(err, ErrNoLocal) {
		t.Fatalf("无本地备份：%v", err)
	}
	f.local.list = []model.BackupInfo{{Name: localNew}}
	f.mem.ensureErr = NewError(ReasonConnect, "连不上", nil)
	_, err := f.svc.UploadNow(context.Background())
	var re *Error
	if !errors.As(err, &re) || re.Reason != ReasonConnect {
		t.Fatalf("远端失败：%v", err)
	}
	if ups := f.uploads(t); len(ups) != 1 || ups[0].Success {
		t.Fatalf("失败应写入记录：%+v", ups)
	}
	// 服务关停后不再接受上传。
	f.cancel()
	<-f.done
	f.mem.ensureErr = nil
	if _, err := f.svc.UploadNow(context.Background()); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("关停后：%v", err)
	}
}

func TestUploadNowConfigErrorBecomesFieldErrors(t *testing.T) {
	f := newSvc(t)
	f.enable(t)
	f.svc.cfg.NewTarget = func(Config) (Target, error) {
		return nil, configErr("smb_host", model.FieldInvalid, "SMB 主机地址无效")
	}
	_, err := f.svc.UploadNow(context.Background())
	fe := fieldErr(t, err)
	if fe["smb_host"] != model.FieldInvalid {
		t.Fatalf("fe = %v", fe)
	}
	if ups := f.uploads(t); len(ups) != 1 || ups[0].Success || !strings.HasPrefix(ups[0].Reason, "配置无效") {
		t.Fatalf("记录 = %+v", ups)
	}
}

// blockingPut 让 Put 阻塞到 release 关闭（或 ctx 结束），并统计同时进行的 Put 数。
type blockingPut struct {
	release chan struct{}
	started chan struct{}
	active  atomic.Int64
	peak    atomic.Int64
}

func newBlocking(m *memTarget) *blockingPut {
	b := &blockingPut{release: make(chan struct{}), started: make(chan struct{}, 8)}
	m.putHook = func(ctx context.Context, name string, r io.Reader) error {
		cur := b.active.Add(1)
		defer b.active.Add(-1)
		for {
			p := b.peak.Load()
			if cur <= p || b.peak.CompareAndSwap(p, cur) {
				break
			}
		}
		b.started <- struct{}{}
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		m.mu.Lock()
		m.files[name] = data
		m.mu.Unlock()
		return nil
	}
	return b
}

func waitStarted(t *testing.T, b *blockingPut) {
	t.Helper()
	select {
	case <-b.started:
	case <-time.After(5 * time.Second):
		t.Fatal("上传没有开始")
	}
}

func TestUploadsAreSerialized(t *testing.T) {
	f := newSvc(t)
	f.enable(t)
	b := newBlocking(f.mem)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.svc.UploadNow(context.Background())
			errs <- err
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		f.svc.AfterDaily(context.Background(), f.local.list[0])
	}()
	waitStarted(t, b)
	// 第一个上传卡住时，其余两个在锁上排队，不会开始 Put。
	select {
	case <-b.started:
		t.Fatal("上传应串行，第二个不应在第一个结束前开始")
	case <-time.After(100 * time.Millisecond):
	}
	close(b.release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("排队的上传应成功：%v", err)
		}
	}
	if b.peak.Load() != 1 {
		t.Fatalf("同时进行的上传峰值 = %d，期望 1", b.peak.Load())
	}
	if n := len(f.uploads(t)); n != 3 {
		t.Fatalf("应有 3 条记录，得到 %d", n)
	}
}

func TestUploadNowContinuesAfterRequestCancelled(t *testing.T) {
	f := newSvc(t)
	f.enable(t)
	b := newBlocking(f.mem)
	reqCtx, cancelReq := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := f.svc.UploadNow(reqCtx)
		errc <- err
	}()
	waitStarted(t, b)
	cancelReq()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("请求取消后应返回 Canceled：%v", err)
	}
	close(b.release)
	deadline := time.Now().Add(5 * time.Second)
	for len(f.uploads(t)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("请求断开后上传应继续并写入记录")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ups := f.uploads(t); !ups[0].Success {
		t.Fatalf("记录 = %+v", ups[0])
	}
}

func TestShutdownCancelsInflightUploadAndWaits(t *testing.T) {
	f := newSvc(t)
	f.enable(t)
	b := newBlocking(f.mem)
	errc := make(chan error, 1)
	go func() {
		_, err := f.svc.UploadNow(context.Background())
		errc <- err
	}()
	waitStarted(t, b)
	f.cancel()
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		t.Fatal("关停后应等进行中的上传退出")
	}
	if err := <-errc; !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("关停取消的上传应返回 ErrShuttingDown：%v", err)
	}
	ups := f.uploads(t)
	if len(ups) != 1 || ups[0].Success || !strings.Contains(ups[0].Reason, "已取消") {
		t.Fatalf("取消也应写入失败记录：%+v", ups)
	}
}

func TestTestConnection(t *testing.T) {
	f := newSvc(t)
	in := smbInput()
	in.Enabled = false // 未启用也允许测试，且不落库
	res, err := f.svc.Test(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.TargetType != "smb" {
		t.Fatalf("res = %+v", res)
	}
	if len(f.mem.putSeen) != 1 || !strings.HasPrefix(f.mem.putSeen[0], writeTestPrefix) {
		t.Fatalf("应写入测试文件：%v", f.mem.putSeen)
	}
	if got := f.mem.names(); len(got) != 0 {
		t.Fatalf("测试文件应已删除：%v", got)
	}
	if got, _ := f.repo.Get(context.Background()); got.UpdatedAt != nil {
		t.Fatal("测试连接不应保存配置")
	}

	f.mem.ensureErr = NewError(ReasonNotWritable, "无法创建目录", nil)
	_, err = f.svc.Test(context.Background(), in)
	var re *Error
	if !errors.As(err, &re) || re.Reason != ReasonNotWritable {
		t.Fatalf("目录不可写：%v", err)
	}

	in.Keep = 0
	if _, err := f.svc.Test(context.Background(), in); fieldErr(t, err)["keep"] != model.FieldOutOfRange {
		t.Fatalf("校验失败应返回字段错误：%v", err)
	}
}

func TestTestConnectionConstructorErrorIsFieldError(t *testing.T) {
	repo, _, clk := newRepo(t)
	svc := NewService(ServiceConfig{Repo: repo, Local: &fakeLocal{}, Clock: clk})
	// 未启用时类型可空，Resolve 通过，构造目标时报 type 必填。
	_, err := svc.Test(context.Background(), model.BackupRemoteInput{Keep: 30})
	if fieldErr(t, err)["type"] != model.FieldRequired {
		t.Fatalf("err = %v", err)
	}
}

func TestStatus(t *testing.T) {
	f := newSvc(t)
	ctx := context.Background()
	st, err := f.svc.Status(ctx)
	if err != nil || st.Enabled {
		t.Fatalf("未启用：%+v %v", st, err)
	}
	f.enable(t)
	st, _ = f.svc.Status(ctx)
	if !st.Enabled || st.LastAttempt != nil || st.LastSuccess != nil || st.LastFailed {
		t.Fatalf("启用未上传：%+v", st)
	}
	if _, err := f.svc.UploadNow(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ = f.svc.Status(ctx)
	if st.LastSuccess == nil || st.LastAttempt == nil || st.LastFailed {
		t.Fatalf("成功后：%+v", st)
	}
	f.mem.putErr = NewError(ReasonIO, "断开", nil)
	_, _ = f.svc.UploadNow(ctx)
	st, _ = f.svc.Status(ctx)
	if !st.LastFailed || st.LastSuccess == nil {
		t.Fatalf("失败后：%+v", st)
	}
}
