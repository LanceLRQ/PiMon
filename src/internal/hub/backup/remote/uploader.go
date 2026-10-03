package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/backup"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

const (
	tempPrefix = ".pimon-upload-"
	tempSuffix = ".part"
	// cleanupTimeout 是失败后删除临时文件这类收尾动作的时限。
	cleanupTimeout = 15 * time.Second
)

var tempPattern = regexp.MustCompile(`^\.pimon-upload-.+\.part$`)

// Uploader 编排一次加密上传：加密流 → 临时名上传 → 改名为正式名 → 清理残留 → 远端只保留最近 N 份。
type Uploader struct {
	Clock      clock.Clock
	Passphrase string
	// WorkFactor 是 scrypt 的 log2(N)；0 为 age 默认强度，仅测试传较小值。
	WorkFactor int
	// Stall 是无进展超时，0 表示 StallTimeout。
	Stall time.Duration
}

// Result 是一次上传的结果。
type Result struct {
	// RemoteName 是远端正式文件名。
	RemoteName string
	// Bytes 是上传的密文字节数。
	Bytes int64
	// CleanupErr 是上传成功后清理过期备份时遇到的错误；上传本身已成功，由调用方决定如何记录。
	CleanupErr error
}

// Upload 把 localPath（本地备份，文件名 name 须符合本项目备份命名）加密后上传到 target，
// 远端正式名为 name + ".age"，同名已存在时覆盖。keep 是远端保留的份数（含本次，至少 1）。
// 清理只涉及符合本项目命名的加密备份与自己的 .pimon-upload-*.part 残留，不碰目录里的其他文件。
// 调用方取消 ctx 时返回 ctx 的错误；其余失败返回 *Error。上传阶段及之后失败时，Result 仍带远端名与
// 已写入的密文字节数，供上传记录使用。
func (u *Uploader) Upload(ctx context.Context, target Target, localPath, name string, keep int) (Result, error) {
	remoteName, ok := backup.RemoteName(name)
	if !ok {
		return Result{}, fmt.Errorf("不是备份文件名: %q", name)
	}
	if keep < 1 {
		return Result{}, fmt.Errorf("保留份数必须至少为 1，得到 %d", keep)
	}
	f, err := os.Open(localPath)
	if err != nil {
		return Result{}, fmt.Errorf("打开本地备份: %w", err)
	}
	defer func() { _ = f.Close() }()

	enc, err := backup.EncryptReader(f, u.Passphrase, u.WorkFactor)
	if err != nil {
		return Result{}, err
	}

	if err := target.EnsureDir(ctx); err != nil {
		return Result{}, classifyCtx(ctx, err)
	}
	// 上次中断留下的临时文件是本项目自己的，先清掉。
	if err := removeStaleTemps(ctx, target); err != nil {
		return Result{}, classifyCtx(ctx, err)
	}

	tmpName := tempPrefix + remoteName + tempSuffix
	size, err := u.put(ctx, target, tmpName, enc)
	res := Result{RemoteName: remoteName, Bytes: size}
	if err != nil {
		removeBestEffort(target, tmpName)
		return res, err
	}
	if err := target.Rename(ctx, tmpName, remoteName); err != nil {
		removeBestEffort(target, tmpName)
		return res, classifyCtx(ctx, err)
	}
	res.CleanupErr = pruneOld(ctx, target, keep)
	return res, nil
}

// put 在无进展看门狗保护下上传 src，返回已读出交给目标的字节数（失败时也返回已写入的部分）。
func (u *Uploader) put(ctx context.Context, target Target, tmpName string, src io.Reader) (int64, error) {
	stall := u.Stall
	if stall <= 0 {
		stall = StallTimeout
	}
	putCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	pr := &progressReader{r: src, clk: u.Clock}
	pr.mark()
	done := make(chan struct{})
	var stalled bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stalled = watchStall(putCtx, u.Clock, pr, stall, cancel, done)
	}()
	err := target.Put(putCtx, tmpName, pr)
	close(done)
	wg.Wait()

	n := pr.total()
	switch {
	case ctx.Err() != nil:
		return n, ctx.Err()
	case stalled:
		return n, NewError(ReasonIO, fmt.Sprintf("上传超过 %s 没有进展，已中止", stall), err)
	case err != nil:
		return n, Classify(err)
	}
	return n, nil
}

// watchStall 在 stall 时长内没有读取进展时调用 cancel，返回是否因无进展而取消。
func watchStall(ctx context.Context, clk clock.Clock, pr *progressReader, stall time.Duration, cancel context.CancelFunc, done <-chan struct{}) bool {
	for {
		remaining := stall - clk.Now().Sub(pr.last())
		if remaining <= 0 {
			cancel()
			return true
		}
		select {
		case <-clk.After(remaining):
		case <-done:
			return false
		case <-ctx.Done():
			return false
		}
	}
}

// progressReader 记录最近一次读到数据的时间与累计字节数。
type progressReader struct {
	r   io.Reader
	clk clock.Clock

	mu    sync.Mutex
	at    time.Time
	bytes int64
}

func (p *progressReader) mark() {
	p.mu.Lock()
	p.at = p.clk.Now()
	p.mu.Unlock()
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.mu.Lock()
		p.bytes += int64(n)
		p.at = p.clk.Now()
		p.mu.Unlock()
	}
	return n, err
}

func (p *progressReader) last() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.at
}

func (p *progressReader) total() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bytes
}

func classifyCtx(ctx context.Context, err error) error {
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return ctx.Err()
	}
	return Classify(err)
}

func removeStaleTemps(ctx context.Context, target Target) error {
	entries, err := target.List(ctx)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir && tempPattern.MatchString(e.Name) {
			if err := target.Remove(ctx, e.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// pruneOld 只保留符合本项目命名的加密备份中最近的 keep 份，按文件名里的时间排序，不看服务器 mtime。
// 单个文件删除失败不中断，其余照常清理，返回第一个错误。
func pruneOld(ctx context.Context, target Target, keep int) error {
	entries, err := target.List(ctx)
	if err != nil {
		return classifyCtx(ctx, err)
	}
	type item struct {
		name string
		at   time.Time
	}
	var items []item
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		if at, ok := backup.ParseRemoteName(e.Name); ok {
			items = append(items, item{e.Name, at})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].at.Equal(items[j].at) {
			return items[i].at.After(items[j].at)
		}
		return items[i].name > items[j].name
	})
	var first error
	for i := keep; i < len(items); i++ {
		if err := target.Remove(ctx, items[i].name); err != nil && first == nil {
			first = classifyCtx(ctx, err)
		}
	}
	return first
}

func removeBestEffort(target Target, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	_ = target.Remove(ctx, name)
}
