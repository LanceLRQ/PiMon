package app

import (
	"io"
	"os"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/tlscert"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

// Option 调整 App 的可注入依赖，主要供测试使用。
type Option func(*options)

type options struct {
	clk      clock.Clock
	version  string
	params   auth.Params
	getenv   func(string) string
	stderr   io.Writer
	tlsEnv   tlscert.Env
	onListen func(addr string)
}

func defaultOptions() options {
	return options{
		clk:     clock.Real{},
		version: version.Version,
		params:  auth.DefaultParams,
		getenv:  os.Getenv,
		stderr:  os.Stderr,
	}
}

// WithClock 注入时钟。
func WithClock(c clock.Clock) Option { return func(o *options) { o.clk = c } }

// WithVersion 注入当前版本号，用于判断是否需要升级前备份。
func WithVersion(v string) Option { return func(o *options) { o.version = v } }

// WithHasherParams 注入密码哈希参数（测试用低成本参数）。
func WithHasherParams(p auth.Params) Option { return func(o *options) { o.params = p } }

// WithGetenv 注入环境变量读取函数（systemd 通知、时区探测使用）。
func WithGetenv(f func(string) string) Option { return func(o *options) { o.getenv = f } }

// WithStderr 注入 stderr，设置码等需要让人看到的提示写到这里。
func WithStderr(w io.Writer) Option { return func(o *options) { o.stderr = w } }

// WithTLSEnv 注入自签名证书 SAN 的环境来源。
func WithTLSEnv(e tlscert.Env) Option { return func(o *options) { o.tlsEnv = e } }

// WithOnListen 注入监听成功后的回调，参数为实际监听地址（端口为 0 时用于得知端口）。
func WithOnListen(f func(addr string)) Option { return func(o *options) { o.onListen = f } }
