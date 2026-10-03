package install

import (
	"context"
	"io"
	"net"

	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

// 下沉到 sysinstall 的类型，这里保留原名别名。
type (
	Runner    = sysinstall.Runner
	ExitError = sysinstall.ExitError
	Owner     = sysinstall.Owner
	FileInfo  = sysinstall.FileInfo
	FS        = sysinstall.FS
	User      = sysinstall.User
	Users     = sysinstall.Users
	Listener  = sysinstall.Listener
	Ports     = sysinstall.Ports
)

// ErrNotFound 表示用户或组不存在。
var ErrNotFound = sysinstall.ErrNotFound

// Deps 汇集 install 的全部外部依赖，测试里整体替换。
type Deps struct {
	Out      io.Writer
	Clock    clock.Clock
	Runner   Runner
	FS       FS
	Users    Users
	Ports    Ports
	Health   func(ctx context.Context, url string) error // 单次探测，成功返回 nil
	EUID     int
	GOOS     string
	Self     string // 当前可执行文件路径（已解析符号链接）
	LocalIPs func() []net.IP
}

// Options 是 install 的命令行选项。
type Options struct {
	DesktopUser string // 空表示自动识别 lightdm 自动登录用户
	Kiosk       bool   // 同时安装桌面会话里的 kiosk 配置（必须有桌面用户）
}
