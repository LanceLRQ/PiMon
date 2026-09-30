package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// 备份原因。
const (
	ReasonDaily      = "daily"
	ReasonManual     = "manual"
	ReasonPreUpgrade = "pre-upgrade"
)

const (
	entryDB     = "pimon.db"
	entrySecret = "secret.key"

	nameTimeLayout = "20060102-150405"
)

var (
	// ErrNotFound 表示备份名不合法或文件不存在。
	ErrNotFound = errors.New("备份不存在")
	// ErrExists 表示同一秒内已有同原因的备份。
	ErrExists = errors.New("同名备份已存在")

	namePattern = regexp.MustCompile(`^pimon-backup-(\d{8}-\d{6})-(daily|manual|pre-upgrade)\.tar\.gz$`)
)

// Config 是 Service 的构造参数。
type Config struct {
	DB         *store.DB
	Clock      clock.Clock
	SecretPath string // 密钥文件
	Dir        string // 备份目录，不存在时创建
	// Settings 返回当前设置；时区、每日时刻与保留份数在线可改，因此每次取最新值。
	Settings func() model.Settings
}

// Service 提供备份的创建、列表、清理、下载与每日调度。
type Service struct {
	cfg Config
	// createMu 串行化 Create，手动备份与每日备份并发时不会互相踩文件。
	createMu sync.Mutex
}

// New 创建 Service。
func New(cfg Config) *Service { return &Service{cfg: cfg} }

func fileName(t time.Time, reason string) string {
	return "pimon-backup-" + t.UTC().Format(nameTimeLayout) + "-" + reason + ".tar.gz"
}

// parseName 校验文件名并解析出时间与原因。
func parseName(name string) (time.Time, string, bool) {
	m := namePattern.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, "", false
	}
	t, err := time.ParseInLocation(nameTimeLayout, m[1], time.UTC)
	if err != nil {
		return time.Time{}, "", false
	}
	return t, m[2], true
}

// Create 生成一份备份：先 VACUUM INTO 得到一致快照，再与密钥文件一起打成 tar.gz。
// 同一秒内同原因重复创建返回 ErrExists。
func (s *Service) Create(ctx context.Context, reason string) (model.BackupInfo, error) {
	switch reason {
	case ReasonDaily, ReasonManual, ReasonPreUpgrade:
	default:
		return model.BackupInfo{}, fmt.Errorf("未知备份原因 %q", reason)
	}
	s.createMu.Lock()
	defer s.createMu.Unlock()

	if err := os.MkdirAll(s.cfg.Dir, 0o750); err != nil {
		return model.BackupInfo{}, fmt.Errorf("创建备份目录: %w", err)
	}
	now := s.cfg.Clock.Now().UTC().Truncate(time.Second)
	name := fileName(now, reason)
	final := filepath.Join(s.cfg.Dir, name)
	if _, err := os.Stat(final); err == nil {
		return model.BackupInfo{}, fmt.Errorf("%w: %s", ErrExists, name)
	}

	// 快照放在备份目录下的私有临时目录里，VACUUM INTO 要求目标文件事先不存在。
	snapDir, err := os.MkdirTemp(s.cfg.Dir, ".snapshot-")
	if err != nil {
		return model.BackupInfo{}, fmt.Errorf("创建快照目录: %w", err)
	}
	defer func() { _ = os.RemoveAll(snapDir) }()
	snapPath := filepath.Join(snapDir, entryDB)
	if _, err := s.cfg.DB.ExecContext(ctx, `VACUUM INTO ?`, snapPath); err != nil {
		return model.BackupInfo{}, fmt.Errorf("生成数据库快照: %w", err)
	}

	tmp, err := os.CreateTemp(s.cfg.Dir, ".pimon-backup-*.tmp")
	if err != nil {
		return model.BackupInfo{}, fmt.Errorf("创建临时文件: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return model.BackupInfo{}, err
	}
	if err := writeArchive(tmp, now, map[string]string{entryDB: snapPath, entrySecret: s.cfg.SecretPath}); err != nil {
		return model.BackupInfo{}, err
	}
	if err := tmp.Sync(); err != nil {
		return model.BackupInfo{}, err
	}
	if err := tmp.Close(); err != nil {
		return model.BackupInfo{}, err
	}
	if err := os.Rename(tmpPath, final); err != nil {
		return model.BackupInfo{}, fmt.Errorf("发布备份文件: %w", err)
	}
	committed = true

	st, err := os.Stat(final)
	if err != nil {
		return model.BackupInfo{}, err
	}
	return model.BackupInfo{Name: name, Reason: reason, CreatedAt: now, Size: st.Size()}, nil
}

// writeArchive 按固定顺序把 files（条目名 -> 源文件）写成 tar.gz，条目均为相对名的普通文件。
func writeArchive(w io.Writer, modTime time.Time, files map[string]string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, name := range []string{entryDB, entrySecret} {
		if err := addFile(tw, name, files[name], modTime); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func addFile(tw *tar.Writer, name, src string, modTime time.Time) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("读取 %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{Name: name, Mode: 0o600, Size: st.Size(), ModTime: modTime, Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("写入 %s: %w", name, err)
	}
	return nil
}

// List 返回所有合法命名的备份，按时间倒序；没有备份时返回空切片。
func (s *Service) List() ([]model.BackupInfo, error) {
	des, err := os.ReadDir(s.cfg.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []model.BackupInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取备份目录: %w", err)
	}
	list := make([]model.BackupInfo, 0, len(des))
	for _, de := range des {
		t, reason, ok := parseName(de.Name())
		if !ok || !de.Type().IsRegular() {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		list = append(list, model.BackupInfo{Name: de.Name(), Reason: reason, CreatedAt: t, Size: fi.Size()})
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].CreatedAt.After(list[j].CreatedAt)
		}
		return list[i].Name > list[j].Name
	})
	return list, nil
}

// Prune 只保留最新的 keep 份备份，其余删除。keep < 1 时不做任何事，避免误删全部。
func (s *Service) Prune(keep int) error {
	if keep < 1 {
		return nil
	}
	list, err := s.List()
	if err != nil {
		return err
	}
	var errs []error
	for _, b := range list[min(keep, len(list)):] {
		if err := os.Remove(filepath.Join(s.cfg.Dir, b.Name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Open 打开名为 name 的备份。名称必须符合命名规则（同时杜绝路径穿越），
// 不合法或不存在都返回 ErrNotFound。
func (s *Service) Open(name string) (*os.File, model.BackupInfo, error) {
	t, reason, ok := parseName(name)
	if !ok {
		return nil, model.BackupInfo{}, ErrNotFound
	}
	f, err := os.Open(filepath.Join(s.cfg.Dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, model.BackupInfo{}, ErrNotFound
	}
	if err != nil {
		return nil, model.BackupInfo{}, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, model.BackupInfo{}, ErrNotFound
	}
	return f, model.BackupInfo{Name: name, Reason: reason, CreatedAt: t, Size: st.Size()}, nil
}

// RunDaily 按设置里的每日时刻循环备份，直到 ctx 结束。
// 每轮开始时才读取一次设置：修改备份时刻或时区后，要等当前这次等待结束、
// 进入下一轮才会生效。备份或清理失败只记日志，不中断循环。
func (s *Service) RunDaily(ctx context.Context) {
	for {
		cfg := s.cfg.Settings()
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			slog.Warn("备份时区无效，改用 UTC", "timezone", cfg.Timezone, "err", err)
			loc = time.UTC
		}
		now := s.cfg.Clock.Now()
		next, err := NextRun(now, loc, cfg.Backup.DailyAt)
		if err != nil {
			slog.Error("备份时刻无效，一小时后重试", "daily_at", cfg.Backup.DailyAt, "err", err)
			next = now.Add(time.Hour)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.cfg.Clock.After(next.Sub(now)):
		}
		if _, err := s.Create(ctx, ReasonDaily); err != nil {
			slog.Error("每日备份失败", "err", err)
			continue
		}
		if err := s.Prune(s.cfg.Settings().Backup.Keep); err != nil {
			slog.Error("清理旧备份失败", "err", err)
		}
	}
}

// NextRun 返回严格晚于 now 的下一个 loc 时区 hhmm（HH:MM）时刻。
// 恰好等于该时刻时顺延到次日。
func NextRun(now time.Time, loc *time.Location, hhmm string) (time.Time, error) {
	if len(hhmm) != 5 || hhmm[2] != ':' {
		return time.Time{}, fmt.Errorf("每日时刻 %q 不是 HH:MM", hhmm)
	}
	h, errH := strconv.Atoi(hhmm[:2])
	m, errM := strconv.Atoi(hhmm[3:])
	if errH != nil || errM != nil || hhmm[0] < '0' || hhmm[0] > '9' || hhmm[3] < '0' || hhmm[3] > '9' {
		return time.Time{}, fmt.Errorf("每日时刻 %q 不是 HH:MM", hhmm)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return time.Time{}, fmt.Errorf("每日时刻 %q 超出范围", hhmm)
	}
	local := now.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), h, m, 0, 0, loc)
	if !next.After(now) {
		next = time.Date(local.Year(), local.Month(), local.Day()+1, h, m, 0, 0, loc)
	}
	return next, nil
}
