package remote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

var (
	// ErrDisabled 表示远端备份未启用，却请求了上传。
	ErrDisabled = errors.New("远端备份未启用")
	// ErrNoLocal 表示没有可上传的本地备份。
	ErrNoLocal = errors.New("没有可上传的本地备份")
	// ErrShuttingDown 表示服务尚未启动或正在关停，不再接受新的上传。
	ErrShuttingDown = errors.New("远端备份服务未运行或正在关停")
)

const (
	// recordTimeout 是写上传记录的时限；上传因关停被取消时也要把记录写进去。
	recordTimeout = 10 * time.Second
	// writeTestPrefix 是测试连接时写入的临时文件名前缀。
	writeTestPrefix = ".pimon-write-test-"
)

// LocalBackups 是本地备份的只读视图，由 backup.Service 实现。
type LocalBackups interface {
	// List 返回本地备份，按时间倒序。
	List() ([]model.BackupInfo, error)
	// Path 返回备份文件路径；名称不合法或文件不存在时返回错误。
	Path(name string) (string, error)
}

// ServiceConfig 是 Service 的构造参数。
type ServiceConfig struct {
	Repo  *Repo
	Local LocalBackups
	Clock clock.Clock
	// NewTarget 按配置构造目标；为空时用 NewTarget（按类型构造 SMB 或 WebDAV）。测试注入替身。
	NewTarget func(Config) (Target, error)
	// WorkFactor 是 scrypt 的 log2(N)；0 为 age 默认强度，仅测试传较小值。
	WorkFactor int
	// Stall 是上传无进展超时，0 表示 StallTimeout。
	Stall time.Duration
}

// Status 是远端备份的运行状态摘要，供 hub-self 判定「异地备份」数据项。时间取上传结束时间；
// LastAttempt 与 LastFailed 取最近一条非取消记录（被取消的上传不算失败）。
type Status struct {
	Enabled     bool
	LastAttempt *time.Time
	LastSuccess *time.Time
	// LastFailed 表示最近一次（非取消的）上传失败。
	LastFailed bool
}

// Service 负责远端备份的上传（每日任务触发与立即上传）、连接测试与状态查询。
//
// 并发与生命周期：所有上传共用一把锁（sem），同一时刻只有一个上传，后来者排队等待；
// 上传运行在 Start 传入的服务生命周期 ctx 下，不跟随 HTTP 请求的 ctx——客户端或反代断开后上传照常进行并记录，
// 中枢关停（生命周期 ctx 结束）时取消上传，Start 返回的 done 在所有进行中的上传退出后关闭。
type Service struct {
	cfg ServiceConfig
	sem chan struct{}

	mu       sync.Mutex
	life     context.Context
	closed   bool
	inflight sync.WaitGroup
	// queued 是正在锁上排队的上传数，供测试与排障观察。
	queued int
}

// NewService 构造服务；须调用 Start 后才接受上传。
func NewService(cfg ServiceConfig) *Service {
	if cfg.NewTarget == nil {
		cfg.NewTarget = NewTarget
	}
	return &Service{cfg: cfg, sem: make(chan struct{}, 1)}
}

// NewTarget 按配置的类型构造 SMB 或 WebDAV 目标。配置不合法时返回 *ConfigError。
func NewTarget(c Config) (Target, error) {
	switch c.Type {
	case model.BackupRemoteSMB:
		t, err := NewSMB(c.SMB())
		if err != nil {
			return nil, err
		}
		return t, nil
	case model.BackupRemoteWebDAV:
		t, err := NewWebDAV(c.WebDAV())
		if err != nil {
			return nil, err
		}
		return t, nil
	case "":
		return nil, configErr("type", model.FieldRequired, "未选择远端类型")
	default:
		return nil, configErr("type", model.FieldInvalid, "远端类型无效")
	}
}

// Start 以 ctx 作为服务生命周期开始接受上传。ctx 结束后不再接受新的上传、取消进行中的上传，
// 返回的通道在进行中的上传全部退出后关闭。
func (s *Service) Start(ctx context.Context) <-chan struct{} {
	s.mu.Lock()
	s.life = ctx
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.inflight.Wait()
		close(done)
	}()
	return done
}

// begin 登记一个进行中的上传并返回生命周期 ctx；服务未启动或已关停时返回 ErrShuttingDown。
// 在同一把 mu 下检查 closed 与 Add，保证 Start 中的 Wait 开始后不会再有 Add。
func (s *Service) begin() (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.life == nil || s.closed || s.life.Err() != nil {
		return nil, ErrShuttingDown
	}
	s.inflight.Add(1)
	return s.life, nil
}

// lock 获取上传锁，ctx 结束时放弃等待。
func (s *Service) lock(ctx context.Context) error {
	select {
	case s.sem <- struct{}{}:
		return nil
	default:
	}
	s.mu.Lock()
	s.queued++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.queued--
		s.mu.Unlock()
	}()
	select {
	case s.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) unlock() { <-s.sem }

// AfterDaily 是每日本地备份成功后的回调：目标已启用时立即上传刚生成的那份。
// 失败只记录并写日志，不重试（第二天自然再传一次）。ctx 是每日任务的 ctx。
func (s *Service) AfterDaily(ctx context.Context, info model.BackupInfo) {
	if _, err := s.begin(); err != nil {
		return
	}
	defer s.inflight.Done()
	if err := s.lock(ctx); err != nil {
		return
	}
	defer s.unlock()
	cfg, err := s.cfg.Repo.Config(ctx)
	if err != nil {
		slog.Error("读取远端备份配置失败，跳过本次上传", "err", err)
		return
	}
	if !cfg.Enabled {
		return
	}
	_, _ = s.upload(ctx, cfg, info)
}

// UploadNow 立即上传最新的本地备份并同步等待结果。上传运行在服务生命周期 ctx 下：
// reqCtx 结束（客户端断开）时本方法返回 reqCtx 的错误，上传在后台继续并照常记录。
// 已有上传在进行时排队等待。未启用返回 ErrDisabled，没有本地备份返回 ErrNoLocal，
// 远端失败返回 *Error（同时已写入失败记录），配置不合法返回 model.FieldErrors。
func (s *Service) UploadNow(reqCtx context.Context) (model.BackupUpload, error) {
	// 排队前先做一次快速检查，未启用或没有本地备份时不必等锁。
	if _, _, err := s.pick(reqCtx); err != nil {
		return model.BackupUpload{}, err
	}
	life, err := s.begin()
	if err != nil {
		return model.BackupUpload{}, err
	}
	type result struct {
		u   model.BackupUpload
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer s.inflight.Done()
		u, err := s.uploadLatest(life)
		ch <- result{u, err}
	}()
	select {
	case r := <-ch:
		return r.u, r.err
	case <-reqCtx.Done():
		return model.BackupUpload{}, reqCtx.Err()
	}
}

func (s *Service) uploadLatest(ctx context.Context) (model.BackupUpload, error) {
	if err := s.lock(ctx); err != nil {
		return model.BackupUpload{}, ErrShuttingDown
	}
	defer s.unlock()
	// 拿到锁后重新读取：排队期间配置可能被改、可能刚生成了新的本地备份。
	cfg, info, err := s.pick(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return model.BackupUpload{}, ErrShuttingDown
		}
		return model.BackupUpload{}, err
	}
	u, err := s.upload(ctx, cfg, info)
	if err != nil && ctx.Err() != nil {
		return u, ErrShuttingDown
	}
	return u, err
}

// pick 返回当前配置与最新的本地备份。
func (s *Service) pick(ctx context.Context) (Config, model.BackupInfo, error) {
	cfg, err := s.cfg.Repo.Config(ctx)
	if err != nil {
		return Config{}, model.BackupInfo{}, err
	}
	if !cfg.Enabled {
		return Config{}, model.BackupInfo{}, ErrDisabled
	}
	list, err := s.cfg.Local.List()
	if err != nil {
		return Config{}, model.BackupInfo{}, fmt.Errorf("列出本地备份: %w", err)
	}
	if len(list) == 0 {
		return Config{}, model.BackupInfo{}, ErrNoLocal
	}
	return cfg, list[0], nil
}

// upload 执行一次上传（调用方已持有上传锁），写入上传记录与 hub 日志，返回记录。
// 记录的原因为「<机器码>:<说明>」（model.UploadReason*）：本地备份缺失或不可读记 no_local 并返回 ErrNoLocal；
// 配置不合法记 config 并返回字段错误；服务 ctx 结束导致的中止记 canceled（不算失败）并返回 ctx 的错误；
// 远端失败记分类原因并返回 *Error；上传成功但清理远端过期备份失败记 cleanup_failed，仍为成功。
// 记录与日志只含机器码与已脱敏的说明，不含密码与口令。
func (s *Service) upload(ctx context.Context, cfg Config, info model.BackupInfo) (model.BackupUpload, error) {
	remoteName, _ := backup.RemoteName(info.Name)
	rec := model.BackupUpload{Name: remoteName, TargetType: cfg.Type, StartedAt: s.cfg.Clock.Now()}
	var (
		res          Result
		code, detail string
		retErr       error
	)
	if path, err := s.cfg.Local.Path(info.Name); err != nil {
		code, detail, retErr = model.UploadReasonNoLocal, info.Name, ErrNoLocal
	} else if tg, err := s.cfg.NewTarget(cfg); err != nil {
		code, detail, retErr = model.UploadReasonConfig, err.Error(), err
		var ce *ConfigError
		if errors.As(err, &ce) {
			retErr = ce.FieldErrors()
		}
	} else {
		u := &Uploader{Clock: s.cfg.Clock, Passphrase: cfg.Passphrase, WorkFactor: s.cfg.WorkFactor, Stall: s.cfg.Stall}
		var upErr error
		res, upErr = u.Upload(ctx, tg, path, info.Name, cfg.Keep)
		_ = tg.Close()
		var le *LocalError
		switch {
		case upErr == nil:
			if res.CleanupErr != nil {
				re := Classify(res.CleanupErr)
				code, detail = model.UploadReasonCleanupFailed, string(re.Reason)+": "+re.Message
			}
		case ctx.Err() != nil:
			// 只看服务 ctx：它结束才算取消（中枢关停）；目标自身的超时仍按远端失败归类。
			code, retErr = model.UploadReasonCanceled, ctx.Err()
		case errors.As(upErr, &le):
			code, detail, retErr = model.UploadReasonNoLocal, scrub(le.Error()), ErrNoLocal
		default:
			re := Classify(upErr)
			code, detail, retErr = string(re.Reason), re.Message, re
		}
	}
	rec.FinishedAt = s.cfg.Clock.Now()
	rec.Size = res.Bytes
	rec.Success = retErr == nil
	rec.Reason = model.FormatUploadReason(code, detail)
	switch {
	case code == model.UploadReasonCanceled:
		slog.Info("远端备份上传已取消（中枢关停）", "name", rec.Name, "type", rec.TargetType)
	case !rec.Success:
		slog.Warn("远端备份上传失败", "name", rec.Name, "type", rec.TargetType, "reason", code, "detail", detail)
	case code == model.UploadReasonCleanupFailed:
		slog.Warn("远端备份已上传，但清理远端过期备份失败", "name", rec.Name, "type", rec.TargetType,
			"bytes", rec.Size, "detail", detail)
	default:
		slog.Info("远端备份已上传", "name", rec.Name, "type", rec.TargetType, "bytes", rec.Size)
	}
	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	saved, err := s.cfg.Repo.RecordUpload(recCtx, rec)
	if err != nil {
		slog.Error("写入远端备份上传记录失败", "name", rec.Name, "err", err)
		saved = rec
		saved.FillReason()
	}
	return saved, retErr
}

// Test 用请求体中的配置（密钥留空用已保存的值，不落库，未启用也可测）测试连接：
// 连接 → 逐级创建目录 → 写入临时测试文件 → 删除。配置不合法返回 model.FieldErrors，远端失败返回 *Error。
func (s *Service) Test(ctx context.Context, in model.BackupRemoteInput) (model.BackupRemoteTest, error) {
	cfg, err := s.cfg.Repo.Resolve(ctx, in)
	if err != nil {
		return model.BackupRemoteTest{}, err
	}
	tg, err := s.cfg.NewTarget(cfg)
	if err != nil {
		var ce *ConfigError
		if errors.As(err, &ce) {
			return model.BackupRemoteTest{}, ce.FieldErrors()
		}
		return model.BackupRemoteTest{}, err
	}
	defer func() { _ = tg.Close() }()
	start := s.cfg.Clock.Now()
	if err := tg.EnsureDir(ctx); err != nil {
		return model.BackupRemoteTest{}, classifyCtx(ctx, err)
	}
	name := writeTestPrefix + randomHex(8)
	if err := tg.Put(ctx, name, strings.NewReader("PiMon 远端备份写入测试\n")); err != nil {
		removeBestEffort(tg, name)
		return model.BackupRemoteTest{}, classifyCtx(ctx, err)
	}
	if err := tg.Remove(ctx, name); err != nil {
		return model.BackupRemoteTest{}, classifyCtx(ctx, err)
	}
	return model.BackupRemoteTest{
		Success: true, TargetType: cfg.Type,
		ElapsedMS: max(0, s.cfg.Clock.Now().Sub(start).Milliseconds()),
	}, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Status 返回是否启用以及最近一次上传尝试与最近一次成功的时间。
func (s *Service) Status(ctx context.Context) (Status, error) {
	cfg, err := s.cfg.Repo.Get(ctx)
	if err != nil {
		return Status{}, err
	}
	st := Status{Enabled: cfg.Enabled}
	if !st.Enabled {
		return st, nil
	}
	// 「最近一次结果」跳过因关停被取消的记录：例行重启不应让屏幕变红。
	last, err := s.cfg.Repo.LastResult(ctx)
	if err != nil {
		return Status{}, err
	}
	if last != nil {
		at := last.FinishedAt
		st.LastAttempt, st.LastFailed = &at, !last.Success
	}
	ok, err := s.cfg.Repo.LastUpload(ctx, true)
	if err != nil {
		return Status{}, err
	}
	if ok != nil {
		at := ok.FinishedAt
		st.LastSuccess = &at
	}
	return st, nil
}

// Get 返回脱敏后的配置。
func (s *Service) Get(ctx context.Context) (model.BackupRemote, error) { return s.cfg.Repo.Get(ctx) }

// Put 校验并保存配置；改口令只影响之后的上传，响应的 passphrase_changed 提示前端警告口令丢失将无法恢复。
func (s *Service) Put(ctx context.Context, in model.BackupRemoteInput) (model.BackupRemote, error) {
	return s.cfg.Repo.Put(ctx, in)
}

// Uploads 返回最近的上传记录，新到旧。
func (s *Service) Uploads(ctx context.Context, limit int) ([]model.BackupUpload, error) {
	return s.cfg.Repo.Uploads(ctx, limit)
}
