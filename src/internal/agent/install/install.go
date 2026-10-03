package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/certs"
	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

const (
	readyTimeout = 15 * time.Second
	pollEvery    = time.Second
)

// run 保存一次安装过程中跨步骤的状态。
type run struct {
	Deps
	o   Options
	t   target
	rep *sysinstall.Report

	mode        string // config.ModeDialIn / ModeDialOut
	pin         certs.SPKI
	port        int
	existing    *config.Config // 安装前的配置（不存在或无法解析时为 nil）
	existingRaw []byte         // 安装前配置文件的原始内容（不存在时为 nil）
	needPair    bool

	pimon   sysinstall.User
	confOwn *sysinstall.Owner // 写配置的属主：系统模式为 pimon，用户模式沿用当前用户（nil）

	selfIsBin    bool // 当前可执行文件就是安装位置（安装脚本已放好二进制）
	activeBefore bool // 开始时服务已在运行（launchd 为已加载）
	binChanged   bool
	staleBin     bool // 运行中的服务不是当前二进制
	confChanged  bool
	defChanged   bool
	groupChanged bool
}

// Install 按模式矩阵检查环境、校验参数，再按固定顺序执行安装；任何一步失败立即停止，
// 输出原因与已完成的步骤。参数错误返回 UsageError，且不做任何改动。
func Install(ctx context.Context, d Deps, o Options) error {
	if d.Clock == nil {
		d.Clock = clock.Real{}
	}
	r := &run{Deps: d, o: o, rep: sysinstall.NewReport(d.Out)}
	if err := r.validateStatic(); err != nil {
		return err
	}
	t, err := resolveTarget(d, o.User)
	if err != nil {
		r.rep.Failure("检测运行环境", err)
		return fmt.Errorf("install 在「检测运行环境」失败: %w", err)
	}
	r.t = t
	if err := r.loadExisting(); err != nil {
		r.rep.Failure("读取已有配置", err)
		return fmt.Errorf("install 在「读取已有配置」失败: %w", err)
	}
	if err := r.validate(); err != nil {
		return err
	}
	r.rep.Done("检测运行环境", r.envText())

	type step struct {
		name string
		fn   func(context.Context) error
	}
	var steps []step
	if t.kind == kindSystem {
		steps = append(steps, step{"创建服务用户", r.ensureUser})
	}
	steps = append(steps,
		step{"准备目录", r.ensureDirs},
		step{"安装二进制", r.installBinary},
		step{"检查服务状态", r.serviceState},
		step{"检查端口", r.checkPort},
	)
	switch t.kind {
	case kindSystem:
		steps = append(steps, step{"docker 组", r.dockerGroup}, step{"放开 ping_group_range", r.ensureSysctl})
	case kindUser:
		steps = append(steps, step{"检查 ping_group_range", r.pingHint})
	}
	steps = append(steps,
		step{"配对", r.configure},
		step{"写入服务定义", r.writeDef},
		step{"启动服务", r.start},
		step{"等待守护进程就绪", r.waitReady},
	)
	for _, s := range steps {
		if err := s.fn(ctx); err != nil {
			r.rep.Failure(s.name, err)
			return fmt.Errorf("install 在「%s」失败: %w", s.name, err)
		}
	}
	r.printSummary()
	return nil
}

func (r *run) envText() string {
	switch r.t.kind {
	case kindSystem:
		return "Linux + systemd，root，系统模式"
	case kindUser:
		return "Linux + systemd，用户模式"
	}
	return "macOS，LaunchAgent"
}

// loadExisting 读取已有配置。系统模式下配置属主是 pimon，config.Load 的属主校验会拒绝 root，所以这里直接解析。
// 配置目录归 pimon 所有：读之前先 Lstat，不是普通文件（含符号链接）就拒绝，防止被诱导读出其他文件。
func (r *run) loadExisting() error {
	path := r.t.paths.Config
	if err := r.requireRegular(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	raw, err := r.FS.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 %s: %w", path, err)
	}
	r.existingRaw = raw
	var c config.Config
	if json.Unmarshal(raw, &c) == nil && c.Validate() == nil {
		r.existing = &c
	}
	return nil
}

// requireRegular 用 Lstat（不跟随链接）确认 path 是普通文件；不存在时返回包装了 fs.ErrNotExist 的错误。
func (r *run) requireRegular(path string) error {
	fi, err := r.FS.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return err
	case err != nil:
		return fmt.Errorf("检查 %s: %w", path, err)
	case !fi.IsRegular:
		return fmt.Errorf("%s 不是普通文件（可能是符号链接），拒绝读取或改写；请检查后删除它再重试", path)
	}
	return nil
}

// validateStatic 校验不依赖本机状态的参数，在检测运行环境之前执行（参数错误不触碰系统）。
func (r *run) validateStatic() error {
	o := &r.o
	switch {
	case o.Hub != "" && o.Listen != "":
		return UsageError("不能同时指定 --hub（拨入）与 --listen（拨出）")
	case o.Listen != "":
		r.mode = config.ModeDialOut
		return r.parseDialOut()
	case o.Hub != "":
		r.mode = config.ModeDialIn
		return r.parseDialIn()
	}
	return nil
}

// validate 在读取已有配置后确定连接模式：不给 --hub 与 --listen 时沿用已有配置（重跑或升级）；
// 拨入时判断是否需要配对。
func (r *run) validate() error {
	o := &r.o
	ex := r.existing
	if r.mode == "" {
		if ex == nil {
			return UsageError("请指定连接方式：拨入用 --hub wss://<hub 地址>:31418 --fp SHA256:… --code <配对码>；拨出用 --listen :31418")
		}
		r.mode = ex.Mode
		var err error
		if ex.Mode == config.ModeDialIn {
			o.Hub, o.Fingerprint = ex.HubURL, ex.HubPin
			err = r.parseDialIn()
		} else {
			o.Listen = orDefault(ex.Listen, fmt.Sprintf(":%d", defaultPort))
			err = r.parseDialOut()
		}
		if err != nil {
			return UsageError(fmt.Sprintf("已有配置 %s 无效（%v），请显式指定连接参数", r.t.paths.Config, err))
		}
	}
	if r.mode == config.ModeDialOut {
		return nil
	}
	paired := ex != nil && ex.Mode == config.ModeDialIn && ex.Credential != "" && ex.HubURL == o.Hub && samePin(ex.HubPin, r.pin)
	r.needPair = !paired || o.RePair
	if r.needPair && strings.TrimSpace(o.Code) == "" {
		return UsageError("拨入安装需要 --code（在网页「添加设备」生成的一次性配对码）")
	}
	return nil
}

func (r *run) parseDialOut() error {
	o := &r.o
	switch {
	case o.Name != "":
		return UsageError("--name 只用于拨入模式；拨出设备的名称在网页添加设备时填写")
	case o.RePair:
		return UsageError("--re-pair 只用于拨入模式；拨出模式重新配对请执行 pimon-agent pair")
	case o.Code != "" || o.Fingerprint != "":
		return UsageError("--code 与 --fp 只用于拨入模式；拨出模式的配对码由本机 pimon-agent pair 生成")
	}
	host, ps, err := net.SplitHostPort(o.Listen)
	port, perr := strconv.Atoi(ps)
	if err != nil || perr != nil || port < 1 || port > 65535 || strings.ContainsAny(host, " \t") {
		return UsageError(fmt.Sprintf("监听地址 %q 无效，应形如 :31418 或 0.0.0.0:31418", o.Listen))
	}
	r.port = port
	return nil
}

func (r *run) parseDialIn() error {
	o := &r.o
	if o.Fingerprint == "" {
		return UsageError("拨入安装需要 --fp（网页上显示的 hub SPKI 指纹，形如 SHA256:…）")
	}
	pin, err := certs.ParseSPKI(o.Fingerprint)
	if err != nil {
		return UsageError("--fp 无效：" + err.Error())
	}
	r.pin = pin
	u, err := url.Parse(o.Hub)
	if err != nil || u.Scheme != "wss" || u.Host == "" {
		return UsageError(fmt.Sprintf("--hub %q 无效，应形如 wss://192.168.1.10:31418", o.Hub))
	}
	return nil
}

func samePin(text string, pin certs.SPKI) bool {
	p, err := certs.ParseSPKI(text)
	return err == nil && p == pin
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// dirOwner 返回本形态下 agent 自有目录与文件的属主。
func (r *run) dirOwner() sysinstall.Owner {
	if r.t.kind == kindSystem {
		return sysinstall.Owner(r.pimon)
	}
	return sysinstall.Owner{UID: r.EUID, GID: r.EGID}
}

func (r *run) ensureDirs(_ context.Context) error {
	own := r.dirOwner()
	p := r.t.paths
	dirs := []struct {
		path string
		mode fs.FileMode
	}{{p.DataDir, p.DataDirMode()}}
	if r.t.kind == kindSystem {
		// 守护进程要在配置目录里原子改写 agent.json（临时文件 + rename），目录必须归 pimon 所有。
		dirs = append(dirs, struct {
			path string
			mode fs.FileMode
		}{filepath.Dir(p.Config), 0o700})
	} else {
		dirs = append(dirs, struct {
			path string
			mode fs.FileMode
		}{filepath.Dir(p.Bin), 0o700})
	}
	for _, dir := range dirs {
		if err := ensureOwnedDir(r.FS, r.rep, dir.path, dir.mode, own); err != nil {
			return err
		}
	}
	return nil
}

// ensureOwnedDir 确保目录存在且属主、权限正确（已存在但不符时修正）。
func ensureOwnedDir(fsys FS, rep *sysinstall.Report, path string, mode fs.FileMode, own sysinstall.Owner) error {
	desc := fmt.Sprintf("%04o %d:%d", mode, own.UID, own.GID)
	fi, err := fsys.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := fsys.Mkdir(path, mode, own); err != nil {
			return fmt.Errorf("创建 %s: %w", path, err)
		}
		rep.Done("创建目录 "+path, desc)
	case err != nil:
		return fmt.Errorf("检查 %s: %w", path, err)
	case !fi.IsDir:
		return fmt.Errorf("%s 已存在但不是目录", path)
	case fi.UID != own.UID || fi.GID != own.GID || fi.Mode != mode:
		if err := fsys.SetOwnerMode(path, mode, own); err != nil {
			return fmt.Errorf("修正 %s 的属主与权限: %w", path, err)
		}
		rep.Done("修正目录 "+path, "属主与权限已改为 "+desc)
	default:
		rep.Existed("目录 "+path, "")
	}
	return nil
}

// ensureDir 只在目录不存在时创建（用于 ~/Library/LaunchAgents 这类不归 agent 所有的目录，不改已有权限）。
func ensureDir(fsys FS, path string, own sysinstall.Owner) error {
	if _, err := fsys.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("检查 %s: %w", path, err)
	}
	if err := fsys.Mkdir(path, 0o755, own); err != nil {
		return fmt.Errorf("创建 %s: %w", path, err)
	}
	return nil
}

func (r *run) installBinary(_ context.Context) error {
	bin := r.t.paths.Bin
	if filepath.Clean(r.Self) == bin {
		// 安装脚本已把二进制放到安装位置：不能自己读自己再写回去，是否需要重启在检查服务状态时判断。
		r.selfIsBin = true
		r.rep.Existed("二进制 "+bin, "当前可执行文件即安装位置")
		return nil
	}
	src, err := r.FS.ReadFile(r.Self)
	if err != nil {
		return fmt.Errorf("读取当前可执行文件 %s: %w", r.Self, err)
	}
	cur, err := r.FS.ReadFile(bin)
	switch {
	case err == nil && string(cur) == string(src):
		r.rep.Existed("二进制 "+bin, "内容相同")
		return nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("读取 %s: %w", bin, err)
	}
	if werr := r.FS.WriteFile(bin, src, 0o755, nil); werr != nil {
		return fmt.Errorf("写入 %s: %w", bin, werr)
	}
	if err == nil {
		r.binChanged = true
		r.rep.Done("升级二进制 "+bin, "服务将在稍后重启")
	} else {
		r.rep.Done("安装二进制 "+bin, "")
	}
	return nil
}

func (r *run) serviceState(ctx context.Context) error {
	if r.t.kind == kindDarwin {
		loaded, err := launchdLoaded(ctx, r.Deps)
		if err != nil {
			return err
		}
		r.activeBefore = loaded
		// launchd 查不到进程的启动时刻，只能在已加载时重启一次以确保运行的是当前二进制。
		r.staleBin = r.selfIsBin && loaded
		return nil
	}
	out, err := systemctl(ctx, r.Deps, r.t, "is-active", ServiceName)
	var ee *sysinstall.ExitError
	switch {
	case err == nil:
		r.activeBefore = strings.TrimSpace(out) == "active"
	case errors.As(err, &ee):
		r.activeBefore = false // inactive/failed/unknown 均以非零退出
	default:
		return err
	}
	if r.selfIsBin && r.activeBefore {
		r.staleBin = r.serviceOlderThan(ctx, r.t.paths.Bin)
	}
	return nil
}

// serviceOlderThan 判断运行中的服务是否早于 path 的最后修改（取不到任何一项数据时按「否」处理）。
// 服务启动时刻 = 现在 - /proc/uptime + systemd 报告的单调时钟时间戳；留 1 秒容差吸收 uptime 的精度。
func (r *run) serviceOlderThan(ctx context.Context, path string) bool {
	out, err := systemctl(ctx, r.Deps, r.t, "show", "-p", "ActiveEnterTimestampMonotonic", "--value", ServiceName)
	if err != nil {
		return false
	}
	var mono int64
	if _, err := fmt.Sscan(strings.TrimSpace(out), &mono); err != nil || mono <= 0 {
		return false
	}
	b, err := r.FS.ReadFile("/proc/uptime")
	if err != nil {
		return false
	}
	var up float64
	if _, err := fmt.Sscan(string(b), &up); err != nil {
		return false
	}
	fi, err := r.FS.Stat(path)
	if err != nil {
		return false
	}
	started := r.Clock.Now().Add(-time.Duration(up * float64(time.Second))).Add(time.Duration(mono) * time.Microsecond)
	return fi.ModTime.After(started.Add(time.Second))
}

// checkPort 检查拨出监听端口：空闲或被本服务自己占用都放行，其余视为冲突。
// Linux 读 /proc 指出占用进程；macOS 只能试绑定，说不出是谁；无权限单独提示，不当作被占用。
func (r *run) checkPort(_ context.Context) error {
	if r.mode != config.ModeDialOut {
		return nil
	}
	port := r.port
	name := fmt.Sprintf("端口 %d", port)
	if port < 1024 && r.t.kind != kindDarwin {
		r.rep.Warn(name, "低于 1024 的端口需要特权，以普通用户（pimon）运行的守护进程通常无法监听，建议改用 1024 以上的端口")
	}
	ls, err := r.Ports.Listeners(port)
	switch {
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("没有权限检查端口 %d（%v）：低于 1024 的端口需要特权，agent 守护进程以普通用户运行同样无法监听，请改用 1024 以上的端口", port, err)
	case err != nil:
		return fmt.Errorf("检查端口 %d: %w", port, err)
	case len(ls) == 0:
		r.rep.Done(name+" 空闲", "")
		return nil
	}
	if r.t.kind == kindDarwin {
		if r.activeBefore {
			r.rep.Existed(name, "LaunchAgent 已加载，视为本服务占用（macOS 无法查出占用进程）")
			return nil
		}
		return fmt.Errorf("端口 %d 已被占用（macOS 无法查出占用进程，可用 lsof -nP -iTCP:%d -sTCP:LISTEN 查看）", port, port)
	}
	self := r.activeBefore
	for _, l := range ls {
		// 二进制被替换后，运行中进程的 exe 链接带 " (deleted)" 后缀。
		exe, deleted := strings.CutSuffix(l.Exe, " (deleted)")
		if exe != r.t.paths.Bin {
			self = false
		} else if deleted {
			r.staleBin = true
		}
	}
	if self {
		r.rep.Existed(name, ServiceName+" 已在运行")
		return nil
	}
	return fmt.Errorf("端口 %d 已被占用：%s", port, describeListeners(ls))
}

func describeListeners(ls []sysinstall.Listener) string {
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

// writeConfig 以 0600 原子写入配置；内容相同且属主、权限正确时记「已存在」。
// 属主或权限不对时以正确属主重写（临时文件 + rename），不对可能被换成链接的路径做 chown。
func (r *run) writeConfig(c config.Config) error {
	data, err := config.Marshal(c)
	if err != nil {
		return err
	}
	path := r.t.paths.Config
	err = r.requireRegular(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	exists := err == nil
	if exists && string(data) == string(r.existingRaw) {
		fi, err := r.FS.Lstat(path)
		if err != nil {
			return fmt.Errorf("检查 %s: %w", path, err)
		}
		own := r.dirOwner()
		// 用户模式下新文件的属组随系统规则（macOS 继承目录属组），只核对属主。
		if fi.Mode == 0o600 && fi.UID == own.UID && (r.t.kind != kindSystem || fi.GID == own.GID) {
			r.rep.Existed("配置 "+path, "内容相同")
			return nil
		}
		if err := r.FS.WriteFile(path, data, 0o600, r.confOwn); err != nil {
			return fmt.Errorf("重写 %s: %w", path, err)
		}
		r.rep.Done("修正配置 "+path, "属主与权限已改为 0600")
		return nil
	}
	if err := r.FS.WriteFile(path, data, 0o600, r.confOwn); err != nil {
		return fmt.Errorf("写入 %s: %w", path, err)
	}
	r.confChanged = true
	r.rep.Done("写入配置 "+path, "")
	return nil
}

// baseConfig 返回本次要写入的配置：同模式时保留已有的配对信息，切换模式时清除并警告。
func (r *run) baseConfig() config.Config {
	c := config.Config{Mode: r.mode}
	ex := r.existing
	same := ex != nil && ex.Mode == r.mode
	if ex != nil && !same {
		r.rep.Warn("连接模式", fmt.Sprintf("由 %s 改为 %s，原配对信息已清除", ex.Mode, r.mode))
	}
	if same {
		c.Name, c.Role = ex.Name, ex.Role
	}
	switch r.mode {
	case config.ModeDialOut:
		c.Listen = r.o.Listen
		if same {
			c.CredentialHash, c.PrevCredentialHash = ex.CredentialHash, ex.PrevCredentialHash
		}
	case config.ModeDialIn:
		c.HubURL, c.HubPin = r.o.Hub, r.pin.String()
		if same && !r.needPair {
			c.Credential, c.HubHostname = ex.Credential, ex.HubHostname
		}
	}
	return c
}

// waitReady 轮询控制套接字，确认守护进程已启动；超时只警告（服务已启用，可能只是启动慢）。
func (r *run) waitReady(ctx context.Context) error {
	if r.Status == nil {
		return nil
	}
	deadline := r.Clock.Now().Add(readyTimeout)
	for {
		err := r.Status(ctx, r.t.paths.CtlSock)
		if err == nil {
			r.rep.Done("守护进程就绪", r.t.paths.CtlSock)
			return nil
		}
		if !r.Clock.Now().Before(deadline) {
			r.rep.Warn("等待守护进程就绪", fmt.Sprintf("%s 内控制套接字没有应答（%v）；查看日志：%s", readyTimeout, err, r.t.logHint()))
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.Clock.After(pollEvery):
		}
	}
}

func (r *run) printSummary() {
	w := r.Out
	line := strings.Repeat("=", 56)
	pf := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format+"\n", a...) }
	pf("%s", line)
	pf("  PiMon agent 安装完成")
	if r.mode == config.ModeDialIn {
		pf("  模式：拨入（连接 %s）", r.o.Hub)
	} else {
		pf("  模式：拨出（监听 %s）", r.o.Listen)
		pf("  下一步：稍候几秒等服务启动，然后在本机执行 %s 打开 15 分钟配对窗口，", r.t.cliHint("pair"))
		pf("          再按提示在 PiMon 网页添加拨出设备（地址填本机的公网地址）")
	}
	pf("  查看状态：%s", r.t.cliHint("status"))
	switch r.t.kind {
	case kindUser:
		pf("  用户服务默认只在你登录期间运行；要开机即运行，请执行（不会自动执行）：")
		pf("    sudo loginctl enable-linger %s", r.Username)
	case kindDarwin:
		pf("  日志：~/.pimon/agent.log（超过 5 MiB 轮转为 agent.log.1）")
	}
	pf("%s", line)
}
