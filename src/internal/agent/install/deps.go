// Package install 实现 `pimon-agent install` 与 `uninstall`：在 Linux 系统模式（systemd 系统单元，User=pimon）、
// Linux 用户模式（systemd 用户单元）与 macOS（LaunchAgent）下安装或卸载服务，拨入模式安装时完成配对。
// 所有外部依赖（命令、文件系统、用户、端口、配对、时钟）都经 Deps 注入，测试不会触碰真实系统。
package install

import (
	"context"
	"io"
	"os"

	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
	"github.com/LanceLRQ/PiMon/src/internal/agent/daemon/link"
	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
	"github.com/LanceLRQ/PiMon/src/plugins/storagehealth/sudocmd"
)

const (
	// ServiceName 是 systemd 单元名（系统模式与 Linux 用户模式相同）。
	ServiceName = "pimon-agent.service"
	// LaunchdLabel 是 macOS LaunchAgent 的 label，plist 文件名为 <label>.plist。
	LaunchdLabel = "io.github.lancelrq.pimon-agent"

	serviceUser    = "pimon"
	systemUnitPath = "/etc/systemd/system/" + ServiceName
	sudoersPath    = "/etc/sudoers.d/pimon-agent" // storage-health 的只读命令白名单，见 sudoers.go
	sysctlPath     = "/etc/sysctl.d/99-pimon-agent.conf"
	pingRangeProc  = "/proc/sys/net/ipv4/ping_group_range"
	dockerSock     = "/var/run/docker.sock"
	groupFile      = "/etc/group"
	defaultPort    = 31418

	cmdSystemctl = "/usr/bin/systemctl"
	cmdUseradd   = "/usr/sbin/useradd"
	cmdUsermod   = "/usr/sbin/usermod"
	cmdSysctl    = "/usr/sbin/sysctl"
	cmdLaunchctl = "/bin/launchctl"
)

// FS 在 sysinstall.FS 之上增加卸载要用的删除操作。
type FS interface {
	sysinstall.FS
	// Remove 删除单个文件；不存在时返回 fs.ErrNotExist。
	Remove(name string) error
	// RemoveAll 递归删除目录；不存在时返回 nil。
	RemoveAll(name string) error
}

// Deps 汇集 install/uninstall/Restart 的全部外部依赖，测试里整体替换。
type Deps struct {
	Out io.Writer
	// In 与 Interactive 用于交互询问（Docker 组、sudoers）；Interactive 为假时从不读 In。
	In          io.Reader
	Interactive bool
	Clock       clock.Clock
	Runner      sysinstall.Runner
	FS          FS
	Users       sysinstall.Users
	// SudoFS 用于解析 storage-health 需要 sudo 的命令路径（含属主校验）。
	SudoFS     sudocmd.FS
	Ports      sysinstall.Ports
	GOOS       string
	EUID, EGID int
	// Home 与 Username 是当前用户的家目录与用户名（用户模式的路径、loginctl 提示）。
	Home     string
	Username string
	// Self 是当前可执行文件路径（已解析符号链接）。
	Self string
	// Pair 执行拨入一次性配对，真实实现为 link.PairDialIn。
	Pair func(ctx context.Context, req link.PairRequest) (link.PairResult, error)
	// Hello 返回配对会话上发出的 hello（含可用插件 manifest），由入口组装。
	Hello func() agent.Hello
	// Status 查询守护进程是否已在控制套接字上应答（等待就绪用）。
	Status func(ctx context.Context, sock string) error
}

// osFS 是 FS 的真实实现。
type osFS struct{ sysinstall.OSFS }

func (osFS) Remove(name string) error    { return os.Remove(name) }
func (osFS) RemoveAll(name string) error { return os.RemoveAll(name) }

// NewDeps 返回使用真实系统的依赖；Hello 由调用方设置（组装 hello 需要插件注册表，install 包不依赖它）。
func NewDeps(out io.Writer, in io.Reader, interactive bool, goos, home, username, self string) Deps {
	return Deps{
		Out: out, In: in, Interactive: interactive,
		Clock: clock.Real{}, Runner: sysinstall.ExecRunner{}, FS: osFS{}, Users: sysinstall.OSUsers{},
		SudoFS: sudocmd.SystemFS(),
		Ports:  sysinstall.NewPorts(goos), GOOS: goos, EUID: os.Geteuid(), EGID: os.Getegid(),
		Home: home, Username: username, Self: self,
		Pair: link.PairDialIn,
		Status: func(ctx context.Context, sock string) error {
			_, err := ctl.Client{Path: sock}.Status(ctx)
			return err
		},
	}
}

// Options 是 install 的命令行选项。
type Options struct {
	// User 对应 --user：Linux 用户模式（macOS 总是用户模式）。
	User bool
	// 拨入：Hub（wss://<IP>:31418）、Fingerprint（hub SPKI 指纹）、Code（一次性配对码）。
	Hub, Fingerprint, Code string
	// Name 写进 pair 帧，网页生成配对码时没有预设名称才会被 hub 采用；只用于拨入。
	Name string
	// RePair 已配对同一 hub 时仍用 Code 重新配对。
	RePair bool
	// Listen 是拨出模式的监听地址（如 :31418）。
	Listen string
	// DockerGroup 非交互时把 pimon 加入 docker 组（交互时不给则询问）。
	DockerGroup bool
	// Sudoers 非交互时写入 storage-health 的 sudoers 白名单（交互时不给则询问）；只用于系统模式。
	Sudoers bool
	// NoService 只做准备目录、安装二进制、配对与写配置，跳过一切系统服务相关步骤（服务状态、服务定义、
	// 启动、等待就绪，以及系统模式的建用户、docker 组、sysctl、sudoers）；只用于用户模式，之后用 run 前台运行。
	NoService bool
}

// UninstallOptions 是 uninstall 的命令行选项。
type UninstallOptions struct {
	User  bool
	Purge bool // 同时删除配置与数据目录
}

// UsageError 表示命令行参数错误（退出码 2）。
type UsageError string

func (e UsageError) Error() string { return string(e) }
