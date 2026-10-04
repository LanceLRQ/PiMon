// Package upgrade 实现 `pimon-agent upgrade`：拨入模式经控制套接字向 hub 取升级信息（版本与校验和走已钉扎的
// agent 通道），从 hub 网页端口下载（http 失败改用钉扎 https）；`--release <tag>` 从 GitHub Releases 下载
// 二进制与 SHA256SUMS（可经 --proxy）。下载完才校验、校验通过才替换（同目录临时文件 + rename），
// 替换后重启服务；任一步失败原二进制保持不动。网络、命令执行与路径都经 Deps 注入。
package upgrade

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
	"github.com/LanceLRQ/PiMon/src/internal/agent/install"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

const (
	// DefaultReleaseBase 是 GitHub Releases 下载地址前缀，资产地址为 <前缀>/<tag>/pimon-agent-<os>-<arch>。
	DefaultReleaseBase = "https://github.com/LanceLRQ/PiMon/releases/download"
	// DefaultMaxSize 是二进制下载的大小上限（200 MiB）。
	DefaultMaxSize int64 = 200 << 20
)

// Deps 汇集升级的外部依赖，测试里整体替换。
type Deps struct {
	Out          io.Writer
	GOOS, GOARCH string
	EUID         int
	// Version 是本机 agent 的版本号。
	Version string
	// Paths 是本机运行模式的路径；被替换的是 Paths.Bin（服务实际运行的二进制）。
	Paths config.Paths
	// LoadConfig 读取本地配置（判断拨入/拨出、取 hub 钉扎指纹）。
	LoadConfig func() (config.Config, error)
	// UpgradeInfo 经控制套接字让守护进程向 hub 请求升级信息。
	UpgradeInfo func(ctx context.Context) (ctl.UpgradeInfo, error)
	// Runner 执行重启服务的命令。
	Runner sysinstall.Runner
	// ReleaseBase 是 Releases 下载地址前缀（测试指向假服务）。
	ReleaseBase string
	// MaxSize 是二进制下载上限，<=0 用 DefaultMaxSize。
	MaxSize int64
}

// NewDeps 返回使用真实系统的依赖。
func NewDeps(out io.Writer, goos string, p config.Paths) Deps {
	euid := os.Geteuid()
	return Deps{
		Out: out, GOOS: goos, GOARCH: goruntime.GOARCH, EUID: euid, Version: version.Version, Paths: p,
		LoadConfig: func() (config.Config, error) { return config.Load(p.Config, euid) },
		UpgradeInfo: func(ctx context.Context) (ctl.UpgradeInfo, error) {
			return ctl.Client{Path: p.CtlSock}.UpgradeInfo(ctx)
		},
		Runner:      sysinstall.ExecRunner{},
		ReleaseBase: DefaultReleaseBase,
		MaxSize:     DefaultMaxSize,
	}
}

func (d Deps) maxSize() int64 {
	if d.MaxSize <= 0 {
		return DefaultMaxSize
	}
	return d.MaxSize
}

func (d Deps) platform() string { return d.GOOS + "-" + d.GOARCH }

// Options 是 upgrade 的命令行选项。
type Options struct {
	// Release 是 GitHub Release 的 tag（vX.Y.Z）；为空时从 hub 升级（仅拨入模式）。
	Release string
	// Proxy 是下载 Release 用的代理 URL（仅与 Release 一起使用）。
	Proxy string
}

// Run 执行一次升级。已是最新时打印「无需升级」并返回 nil；参数错误返回 install.UsageError。
func Run(ctx context.Context, d Deps, o Options) error {
	var px *proxy.Proxy
	if o.Release != "" || o.Proxy != "" {
		if o.Release == "" {
			return install.UsageError("--proxy 只能与 --release 一起使用")
		}
		if !ValidTag(o.Release) {
			return install.UsageError(fmt.Sprintf("--release %q 格式不对，应为 vX.Y.Z（如 v0.2.0）", clip(o.Release)))
		}
		var err error
		if px, err = proxy.Parse(o.Proxy); err != nil {
			return install.UsageError("--proxy 无效：" + err.Error())
		}
	}
	switch {
	case d.Paths.System && d.EUID != 0:
		return errors.New("系统模式的二进制属主是 root，请用 sudo pimon-agent upgrade 执行")
	case !d.Paths.System && d.EUID == 0:
		// 用户模式的服务属于当前登录用户（LaunchAgent / systemd 用户单元），以 root 执行会重启错误的服务域。
		return errors.New("用户模式（macOS 或 --user）请以安装 agent 的普通用户执行，不要用 sudo 或 root")
	}
	target := d.Paths.Bin
	if err := checkTarget(target); err != nil {
		return err
	}
	if o.Release != "" {
		return fromRelease(ctx, d, o.Release, px, target)
	}
	conf, err := d.LoadConfig()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("尚未安装：找不到配置 %s，请先执行 pimon-agent install", d.Paths.Config)
		}
		return err
	}
	if conf.Mode != config.ModeDialIn {
		return install.UsageError("拨出模式请用 --release <tag> [--proxy <URL>] 从 GitHub Releases 升级")
	}
	return fromHub(ctx, d, conf, target)
}

// fromHub 拨入模式：经守护进程取升级信息，从 hub 下载并校验。
func fromHub(ctx context.Context, d Deps, conf config.Config, target string) error {
	info, err := d.UpgradeInfo(ctx)
	if err != nil {
		return fmt.Errorf("获取升级信息: %w", err)
	}
	if info.HubVersion == d.Version {
		say(d.Out, "当前版本 %s 与 hub 相同，无需升级", d.Version)
		return nil
	}
	if info.SHA256 == "" {
		return fmt.Errorf("hub（%s）没有内嵌本平台（%s）的 agent（开发构建常见），无法从 hub 升级；可改用 --release <tag> 从 GitHub Releases 升级",
			clip(info.HubVersion), d.platform())
	}
	want, ok := normalizeSum(info.SHA256)
	if !ok {
		return errors.New("hub 给出的校验和格式不正确")
	}
	pin, err := certs.ParseSPKI(conf.HubPin)
	if err != nil {
		return fmt.Errorf("配置里的 hub 指纹无效: %w", err)
	}
	if skip, err := sameAsTarget(d, target, want); err != nil || skip {
		return err
	}
	t, err := fetchFromHub(ctx, d, info.HubHost, info.WebPort, pin, d.platform(), filepath.Dir(target))
	if err != nil {
		return err
	}
	return applyUpgrade(ctx, d, t, want, target, clip(info.HubVersion))
}

// fromRelease 从 GitHub Releases 下载 SHA256SUMS 与本平台二进制并校验。
func fromRelease(ctx context.Context, d Deps, tag string, px *proxy.Proxy, target string) error {
	if tag == d.Version {
		say(d.Out, "当前版本已是 %s，无需升级", tag)
		return nil
	}
	if !px.IsDirect() {
		say(d.Out, "经代理 %s 下载", px.Redacted())
	}
	c := releaseClient(px)
	base := strings.TrimRight(d.ReleaseBase, "/") + "/" + tag + "/"
	asset := "pimon-agent-" + d.platform()
	say(d.Out, "下载 %sSHA256SUMS", base)
	sums, err := fetchSmall(ctx, c, base+"SHA256SUMS", maxSumsSize)
	if err != nil {
		return fmt.Errorf("下载 SHA256SUMS: %w", err)
	}
	want, err := lookupSum(sums, asset)
	if err != nil {
		return err
	}
	if skip, err := sameAsTarget(d, target, want); err != nil || skip {
		return err
	}
	say(d.Out, "下载 %s%s", base, asset)
	t, err := fetchToTemp(ctx, c, base+asset, filepath.Dir(target), d.maxSize())
	if err != nil {
		return fmt.Errorf("下载 %s: %w", asset, err)
	}
	return applyUpgrade(ctx, d, t, want, target, tag)
}

// sameAsTarget 报告现有二进制是否已与期望内容相同（相同则打印「无需升级」，不下载、不替换）。
func sameAsTarget(d Deps, target, want string) (bool, error) {
	cur, err := fileSum(target)
	if err != nil {
		return false, fmt.Errorf("读取 %s: %w", target, err)
	}
	if cur == want {
		say(d.Out, "%s 内容已与目标版本相同，无需升级", target)
		return true, nil
	}
	return false, nil
}

// applyUpgrade 校验下载内容、替换二进制并重启服务；校验失败删除临时文件，原二进制不动。
func applyUpgrade(ctx context.Context, d Deps, t *tempFile, want, target, ver string) error {
	if t.Sum != want {
		t.discard()
		return fmt.Errorf("校验和不匹配（期望 %s，实际 %s），未替换 %s", want, t.Sum, target)
	}
	say(d.Out, "校验通过（SHA-256 %s）", want)
	if err := t.commit(target); err != nil {
		return err
	}
	say(d.Out, "已把 %s 替换为 %s", target, ver)
	if err := install.Restart(ctx, install.Deps{GOOS: d.GOOS, EUID: d.EUID, Runner: d.Runner}, d.Paths); err != nil {
		return fmt.Errorf("二进制已替换为 %s，但%w；请手动重启 agent 服务", ver, err)
	}
	say(d.Out, "已重启 agent 服务")
	return nil
}

func say(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format+"\n", a...)
}
