package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// exec 执行器的限额。
const (
	// MaxStdoutBytes 是 stdout 上限（1 MiB），超出算失败。
	MaxStdoutBytes = 1 << 20
	// MaxStderrBytes 是 stderr 保留的尾部字节数（16 KiB）。
	MaxStderrBytes = 16 << 10
	// execAPIVersion 是写进 stdin 的 api_version。
	execAPIVersion = 1
	// execWaitDelay 是进程被杀后等待 stdio 管道关闭的宽限。
	execWaitDelay = 2 * time.Second
)

// envWhitelist 是 exec 子进程从 hub 进程继承的环境变量白名单。
// 其余（含 NOTIFY_SOCKET、WATCHDOG_*、各类令牌）一律不传。
var envWhitelist = []string{"PATH", "HOME", "LANG", "TZ"}

// ExecSource 通过运行插件目录下的 run 可执行文件实现 Source。
type ExecSource struct {
	manifest *manifest.Manifest
	runPath  string
}

// NewExecSource 由 manifest 与可执行文件路径构造 exec 形态的 Source。
func NewExecSource(m *manifest.Manifest, runPath string) *ExecSource {
	return &ExecSource{manifest: m, runPath: runPath}
}

// Manifest 返回插件描述。
func (s *ExecSource) Manifest() *manifest.Manifest { return s.manifest }

// execPayload 是写入子进程 stdin 的 JSON。
type execPayload struct {
	APIVersion int               `json:"api_version"`
	Config     map[string]any    `json:"config"`
	Secrets    map[string]string `json:"secrets"`
	Proxy      *string           `json:"proxy"`
	State      string            `json:"state"`
	Last       *report.Report    `json:"last"`
}

func buildPayload(in Input) ([]byte, error) {
	p := execPayload{
		APIVersion: execAPIVersion,
		Config:     in.Config,
		Secrets:    in.Secrets,
		State:      in.State,
		Last:       in.Last,
	}
	if p.Config == nil {
		p.Config = map[string]any{}
	}
	if p.Secrets == nil {
		p.Secrets = map[string]string{}
	}
	if !in.Proxy.IsDirect() {
		u := in.Proxy.URL()
		p.Proxy = &u
	}
	return json.Marshal(p)
}

// buildEnv 只取白名单变量，再追加代理变量（直连时为 NO_PROXY=*）。
func buildEnv(in Input) []string {
	var env []string
	for _, k := range envWhitelist {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return append(env, in.Proxy.Env()...)
}

// limitWriter 累积 stdout，超过上限时标记溢出并触发 onOverflow（用于杀进程组）。
type limitWriter struct {
	mu         sync.Mutex
	max        int
	buf        bytes.Buffer
	overflowed bool
	onOverflow func()
}

func (w *limitWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.overflowed {
		return 0, errOutputTooLarge
	}
	if w.buf.Len()+len(p) > w.max {
		w.overflowed = true
		w.onOverflow()
		return 0, errOutputTooLarge
	}
	return w.buf.Write(p)
}

var errOutputTooLarge = errors.New("stdout 超过上限")

func (w *limitWriter) isOverflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflowed
}

// Collect 运行插件一次：stdin 写输入，stdout 解析为报告。
// 失败分类见 ErrTimeout / ErrFailed；错误信息里的密钥与代理凭据会被抹掉。
func (s *ExecSource) Collect(ctx context.Context, in Input) (*report.Report, error) {
	parent := ctx
	if d := s.manifest.Timeout; d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	rep, err := s.run(ctx, in)
	if err != nil {
		err = classify(parent, ctx, err)
		return nil, redactError(err, in)
	}
	return rep, nil
}

func (s *ExecSource) run(ctx context.Context, in Input) (*report.Report, error) {
	payload, err := buildPayload(in)
	if err != nil {
		return nil, fmt.Errorf("%w: 序列化输入: %w", ErrFailed, err)
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	cmd := exec.CommandContext(runCtx, s.runPath)
	cmd.Dir = filepath.Dir(s.runPath)
	cmd.Env = buildEnv(in)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.WaitDelay = execWaitDelay
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	stdout := &limitWriter{max: MaxStdoutBytes, onOverflow: cancelRun}
	stderr := newTailBuffer(MaxStderrBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: 启动失败: %w", ErrFailed, err)
	}
	waitErr := cmd.Wait()
	// 进程已退出：清理可能残留的同组孙进程。
	_ = killProcessGroup(cmd)

	switch {
	case stdout.isOverflowed():
		return nil, fmt.Errorf("%w: stdout 超过 %d 字节上限", ErrFailed, MaxStdoutBytes)
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case waitErr != nil:
		return nil, fmt.Errorf("%w: %s%s", ErrFailed, describeExit(waitErr), stderrSuffix(stderr))
	}
	rep, err := report.Parse(stdout.buf.Bytes(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: 输出不是合法报告: %w%s", ErrFailed, err, stderrSuffix(stderr))
	}
	return rep, nil
}

func describeExit(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("退出码 %d", ee.ExitCode())
	}
	return err.Error()
}

func stderrSuffix(b *tailBuffer) string {
	text := strings.ToValidUTF8(strings.TrimSpace(b.String()), "")
	if text == "" {
		return ""
	}
	if b.Truncated() {
		return "\nstderr（已截断，仅保留最后 16 KiB）:\n" + text
	}
	return "\nstderr:\n" + text
}

// redactError 抹掉错误文字里的密钥值与代理 URL（含凭据），保留 errors.Is 链。
func redactError(err error, in Input) error {
	secrets := make([]string, 0, len(in.Secrets)+1)
	for _, v := range in.Secrets {
		if v != "" {
			secrets = append(secrets, v)
		}
	}
	if !in.Proxy.IsDirect() {
		if u := in.Proxy.URL(); u != "" {
			secrets = append(secrets, u)
		}
		if pu, err := url.Parse(in.Proxy.URL()); err == nil && pu.User != nil {
			if pw, ok := pu.User.Password(); ok && pw != "" {
				secrets = append(secrets, pw)
			}
		}
	}
	msg := err.Error()
	out := msg
	for _, s := range secrets {
		out = strings.ReplaceAll(out, s, "***")
	}
	if out == msg {
		return err
	}
	return &redactedError{msg: out, cause: err}
}

// redactedError 用脱敏后的文字替换原错误文字，但 Unwrap 保留分类哨兵。
type redactedError struct {
	msg   string
	cause error
}

func (e *redactedError) Error() string { return e.msg }

// Is 只暴露分类哨兵与取消，不暴露可能带明文的原因链。
func (e *redactedError) Is(target error) bool {
	return (target == ErrTimeout || target == ErrFailed || target == context.Canceled || target == context.DeadlineExceeded) && errors.Is(e.cause, target)
}
