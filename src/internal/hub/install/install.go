// Package install 实现 `pimon-hub install`：在树莓派（systemd）上一条命令完成 hub 部署。
// 所有外部依赖（命令、文件系统、用户、端口、HTTP、时钟）都经 Deps 注入，测试不会触碰真实系统。
package install

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/netaddr"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

const (
	serviceUser = "pimon"
	serviceName = "pimon-hub.service"
	hubPort     = 31415
	binPath     = "/usr/local/bin/pimon-hub"
	unitPath    = "/etc/systemd/system/" + serviceName
	dataDir     = "/var/lib/pimon"
	tokenPath   = dataDir + "/screen.token"
	sysctlPath  = "/etc/sysctl.d/99-pimon.conf"
	pingRange   = "/proc/sys/net/ipv4/ping_group_range"
	healthURL   = "http://127.0.0.1:31415/healthz"

	cmdSystemctl = "/usr/bin/systemctl"
	cmdUseradd   = "/usr/sbin/useradd"
	cmdUsermod   = "/usr/sbin/usermod"
	cmdRunuser   = "/usr/sbin/runuser"
	cmdSysctl    = "/usr/sbin/sysctl"

	healthTimeout = 30 * time.Second
	tokenTimeout  = 10 * time.Second
	pollEvery     = time.Second
)

// UsageError 表示命令行参数错误。
type UsageError string

func (e UsageError) Error() string { return string(e) }

// Command 是 `pimon-hub install` 的入口：解析参数并用真实系统依赖执行安装。
func Command(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	desktop := fs.String("desktop-user", "", "桌面用户（默认自动识别 lightdm 自动登录用户）")
	if err := fs.Parse(args); err != nil {
		return UsageError(err.Error())
	}
	if fs.NArg() > 0 {
		return UsageError(fmt.Sprintf("install 不接受位置参数: %v", fs.Args()))
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位当前可执行文件: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	return Run(ctx, Deps{
		Out:      stdout,
		Clock:    clock.Real{},
		Runner:   execRunner{},
		FS:       osFS{},
		Users:    osUsers{},
		Ports:    ProcPorts{Root: "/proc"},
		Health:   httpHealth,
		EUID:     os.Geteuid(),
		GOOS:     runtime.GOOS,
		Self:     self,
		LocalIPs: netaddr.LANIPv4,
	}, Options{DesktopUser: *desktop})
}

// run 保存一次安装过程中跨步骤的状态。
type run struct {
	Deps
	opts Options
	rep  *report

	pimon        User
	upgraded     bool // 二进制被替换
	unitChanged  bool // unit 内容变化
	activeBefore bool // 开始时服务已在运行
	setupCode    string
	setupExpires string
	adminExists  bool
	needRelogin  string // 刚加入 pimon 组的桌面用户
}

// Run 按固定顺序执行安装；任何一步失败立即停止，并输出原因与已完成的步骤。
func Run(ctx context.Context, d Deps, opts Options) error {
	r := &run{Deps: d, opts: opts, rep: &report{out: d.Out}}
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"检测运行环境", r.checkSystem},
		{"创建服务用户与数据目录", r.ensureUserAndDir},
		{"安装二进制", r.installBinary},
		{"检查端口", r.checkPort},
		{"生成首次设置码", r.ensureSetupCode},
		{"写入 systemd 单元", r.writeUnit},
		{"启动服务", r.startService},
		{"等待服务就绪", r.waitReady},
		{"桌面用户加组", r.addDesktopUser},
		{"校验 ping_group_range", r.ensureSysctl},
	}
	for _, s := range steps {
		if err := s.fn(ctx); err != nil {
			r.rep.failure(s.name, err)
			return fmt.Errorf("install 在「%s」失败: %w", s.name, err)
		}
	}
	r.printSummary()
	return nil
}

// poll 反复调用 probe 直到成功或超时，间隔经注入的时钟等待。
func (r *run) poll(ctx context.Context, timeout time.Duration, probe func() error) error {
	deadline := r.Clock.Now().Add(timeout)
	for {
		err := probe()
		if err == nil {
			return nil
		}
		if !r.Clock.Now().Before(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.Clock.After(pollEvery):
		}
	}
}

func (r *run) systemctl(ctx context.Context, args ...string) (string, error) {
	return r.Runner.Run(ctx, append([]string{cmdSystemctl}, args...)...)
}

func (r *run) checkSystem(_ context.Context) error {
	if r.GOOS != "linux" {
		return fmt.Errorf("install 仅支持 Linux（当前为 %s）", r.GOOS)
	}
	if r.EUID != 0 {
		return errors.New("install 必须以 root 运行，请用 sudo ./pimon-hub install")
	}
	fi, err := r.FS.Stat("/run/systemd/system")
	if err != nil || !fi.IsDir {
		return errors.New("未检测到 systemd（/run/systemd/system 不存在），install 只支持 systemd 系统")
	}
	r.rep.done("检测运行环境", "Linux + systemd，root")
	return nil
}

func (r *run) ensureUserAndDir(ctx context.Context) error {
	u, err := r.Users.Lookup(serviceUser)
	switch {
	case err == nil:
		r.rep.existed("系统用户 "+serviceUser, "")
	case errors.Is(err, ErrNotFound):
		if _, err := r.Runner.Run(ctx, cmdUseradd, "--system", "--user-group", "--home-dir", dataDir,
			"--no-create-home", "--shell", "/usr/sbin/nologin", serviceUser); err != nil {
			return fmt.Errorf("创建系统用户 %s: %w", serviceUser, err)
		}
		if u, err = r.Users.Lookup(serviceUser); err != nil {
			return fmt.Errorf("创建后查询用户 %s: %w", serviceUser, err)
		}
		r.rep.done("创建系统用户 "+serviceUser, "")
	default:
		return fmt.Errorf("查询用户 %s: %w", serviceUser, err)
	}
	r.pimon = u

	own := Owner(u)
	fi, err := r.FS.Stat(dataDir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := r.FS.Mkdir(dataDir, 0o750, own); err != nil {
			return fmt.Errorf("创建 %s: %w", dataDir, err)
		}
		r.rep.done("创建数据目录 "+dataDir, "0750 "+serviceUser+":"+serviceUser)
	case err != nil:
		return fmt.Errorf("检查 %s: %w", dataDir, err)
	case !fi.IsDir:
		return fmt.Errorf("%s 已存在但不是目录", dataDir)
	case fi.UID != u.UID || fi.GID != u.GID || fi.Mode != 0o750:
		if err := r.FS.SetOwnerMode(dataDir, 0o750, own); err != nil {
			return fmt.Errorf("修正 %s 的属主与权限: %w", dataDir, err)
		}
		r.rep.done("修正数据目录 "+dataDir, "属主与权限已改为 0750 "+serviceUser+":"+serviceUser)
	default:
		r.rep.existed("数据目录 "+dataDir, "")
	}
	return nil
}

func (r *run) installBinary(_ context.Context) error {
	src, err := r.FS.ReadFile(r.Self)
	if err != nil {
		return fmt.Errorf("读取当前可执行文件 %s: %w", r.Self, err)
	}
	cur, err := r.FS.ReadFile(binPath)
	switch {
	case err == nil && string(cur) == string(src):
		r.rep.existed("二进制 "+binPath, "内容相同")
		return nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("读取 %s: %w", binPath, err)
	}
	if err := r.FS.WriteFile(binPath, src, 0o755, nil); err != nil {
		return fmt.Errorf("写入 %s: %w", binPath, err)
	}
	if err == nil {
		r.upgraded = true
		r.rep.done("升级二进制 "+binPath, "服务将在稍后重启")
	} else {
		r.rep.done("安装二进制 "+binPath, "")
	}
	return nil
}

func (r *run) checkPort(ctx context.Context) error {
	out, err := r.systemctl(ctx, "is-active", serviceName)
	var ee *ExitError
	switch {
	case err == nil:
		r.activeBefore = strings.TrimSpace(out) == "active"
	case errors.As(err, &ee):
		r.activeBefore = false // inactive/failed/unknown 均以非零退出
	default:
		return err
	}
	ls, err := r.Ports.Listeners(hubPort)
	if err != nil {
		return fmt.Errorf("检查端口 %d: %w", hubPort, err)
	}
	if len(ls) == 0 {
		r.rep.done(fmt.Sprintf("端口 %d 空闲", hubPort), "")
		return nil
	}
	self := r.activeBefore
	for _, l := range ls {
		// 二进制被替换后，运行中进程的 exe 链接带 " (deleted)" 后缀。
		if strings.TrimSuffix(l.Exe, " (deleted)") != binPath {
			self = false
		}
	}
	if self {
		r.rep.existed(fmt.Sprintf("端口 %d", hubPort), serviceName+" 已在运行")
		return nil
	}
	return fmt.Errorf("端口 %d 已被占用：%s", hubPort, describeListeners(ls))
}

func describeListeners(ls []Listener) string {
	parts := make([]string, 0, len(ls))
	for _, l := range ls {
		switch {
		case l.PID == 0:
			parts = append(parts, "未知进程（无法解析属主）")
		case l.Exe == "":
			parts = append(parts, fmt.Sprintf("pid %d", l.PID))
		default:
			parts = append(parts, fmt.Sprintf("pid %d（%s）", l.PID, l.Exe))
		}
	}
	return strings.Join(parts, "、")
}

func (r *run) ensureSetupCode(ctx context.Context) error {
	out, err := r.Runner.Run(ctx, cmdRunuser, "-u", serviceUser, "--", binPath, "setup-code", "--if-needed")
	var ee *ExitError
	if errors.As(err, &ee) && ee.Code == 3 {
		r.adminExists = true
		r.rep.skipped("设置码", "已设置管理员")
		return nil
	}
	if err != nil {
		return fmt.Errorf("以 %s 身份生成设置码: %w", serviceUser, err)
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "首次设置码: "); ok {
			r.setupCode = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, "有效期至: "); ok {
			r.setupExpires = strings.TrimSpace(v)
		}
	}
	if r.setupCode == "" {
		return fmt.Errorf("setup-code 的输出里没有设置码: %q", strings.TrimSpace(out))
	}
	r.rep.done("生成首次设置码", "")
	return nil
}

func (r *run) writeUnit(_ context.Context) error {
	cur, err := r.FS.ReadFile(unitPath)
	switch {
	case err == nil && string(cur) == unitContent:
		r.rep.existed("systemd 单元 "+unitPath, "内容相同")
		return nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("读取 %s: %w", unitPath, err)
	}
	if err := r.FS.WriteFile(unitPath, []byte(unitContent), 0o644, nil); err != nil {
		return fmt.Errorf("写入 %s: %w", unitPath, err)
	}
	r.unitChanged = err == nil
	r.rep.done("写入 systemd 单元 "+unitPath, "")
	return nil
}

func (r *run) startService(ctx context.Context) error {
	if _, err := r.systemctl(ctx, "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if _, err := r.systemctl(ctx, "enable", "--now", serviceName); err != nil {
		return fmt.Errorf("enable --now: %w", err)
	}
	r.rep.done("启用并启动 "+serviceName, "")
	// 服务原本没在运行时 enable --now 已经用新二进制与新 unit 启动，无需再重启。
	if r.activeBefore && (r.upgraded || r.unitChanged) {
		if _, err := r.systemctl(ctx, "restart", serviceName); err != nil {
			return fmt.Errorf("restart: %w", err)
		}
		why := "二进制已升级"
		if !r.upgraded {
			why = "单元文件已变化"
		}
		r.rep.done("重启 "+serviceName, why)
	}
	return nil
}

func (r *run) waitReady(ctx context.Context) error {
	err := r.poll(ctx, healthTimeout, func() error { return r.Health(ctx, healthURL) })
	if err != nil {
		return fmt.Errorf("%s 在 %s 内没有就绪（%v）；查看日志：journalctl -u pimon-hub -n 50", healthURL, healthTimeout, err)
	}
	r.rep.done("服务就绪", healthURL)
	err = r.poll(ctx, tokenTimeout, func() error {
		_, err := r.FS.Stat(tokenPath)
		return err
	})
	if err != nil {
		return fmt.Errorf("屏幕令牌 %s 没有出现: %w", tokenPath, err)
	}
	r.rep.done("屏幕令牌", tokenPath)
	return nil
}

func (r *run) ensureSysctl(ctx context.Context) error {
	cur, err := r.FS.ReadFile(pingRange)
	if err == nil {
		var lo, hi int
		if n, _ := fmt.Sscan(string(cur), &lo, &hi); n == 2 && lo <= r.pimon.GID && r.pimon.GID <= hi {
			r.rep.existed("net.ipv4.ping_group_range", "已包含 pimon 组")
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("读取 %s: %w", pingRange, err)
	}
	conf := "net.ipv4.ping_group_range = 0 2147483647\n"
	if err := r.FS.WriteFile(sysctlPath, []byte(conf), 0o644, nil); err != nil {
		return fmt.Errorf("写入 %s: %w", sysctlPath, err)
	}
	if _, err := r.Runner.Run(ctx, cmdSysctl, "-p", sysctlPath); err != nil {
		return fmt.Errorf("应用 %s: %w", sysctlPath, err)
	}
	r.rep.done("放开 ping_group_range", sysctlPath)
	return nil
}

func (r *run) printSummary() {
	w := r.Out
	line := strings.Repeat("=", 56)
	_, _ = fmt.Fprintln(w, line)
	_, _ = fmt.Fprintln(w, "  PiMon 安装完成")
	switch {
	case r.setupCode != "":
		exp := ""
		if r.setupExpires != "" {
			exp = "（有效期至 " + r.setupExpires + "）"
		}
		_, _ = fmt.Fprintf(w, "  首次设置码：%s%s\n", r.setupCode, exp)
	case r.adminExists:
		_, _ = fmt.Fprintln(w, "  管理员已设置，无需设置码；忘记密码请用 reset-password")
	}
	_, _ = fmt.Fprintln(w, "  访问地址：")
	urls := netaddr.CandidateURLs("", "http", fmt.Sprint(hubPort), r.localIPs())
	if len(urls) == 0 {
		urls = []string{fmt.Sprintf("http://<本机地址>:%d", hubPort)}
	}
	for _, u := range urls {
		_, _ = fmt.Fprintf(w, "    %s\n", u)
	}
	if r.needRelogin != "" {
		_, _ = fmt.Fprintf(w, "  注意：用户 %s 刚加入 %s 组，需重新登录桌面会话后才生效\n", r.needRelogin, serviceUser)
	}
	_, _ = fmt.Fprintln(w, line)
}

func (r *run) localIPs() []net.IP {
	if r.LocalIPs == nil {
		return nil
	}
	return r.LocalIPs()
}
