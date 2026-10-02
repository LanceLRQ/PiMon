package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

// Runner 执行外部命令，返回标准输出。命令以非零状态退出时返回 *ExitError。
type Runner interface {
	Run(ctx context.Context, argv ...string) (string, error)
	// RunEnv 与 Run 相同，但子进程环境 = 继承环境去掉全部 PIMON_* 再追加 setEnv（KEY=VALUE 形式）。
	RunEnv(ctx context.Context, setEnv []string, argv ...string) (string, error)
}

// ExitError 表示外部命令以非零状态退出。
type ExitError struct {
	Argv   []string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("%s 退出码 %d", strings.Join(e.Argv, " "), e.Code)
	if s := strings.TrimSpace(e.Stderr); s != "" {
		msg += "：" + s
	}
	return msg
}

// Owner 是文件属主；传 nil 表示不改动属主（沿用创建者，即 root）。
type Owner struct{ UID, GID int }

// FileInfo 是 Stat 的结果；Mode 只含权限位。
type FileInfo struct {
	IsDir   bool
	Mode    fs.FileMode
	ModTime time.Time
	UID     int
	GID     int
}

// FS 是 install 用到的文件系统操作。WriteFile 必须原子（同目录临时文件再 rename），
// 这样替换正在运行的二进制不会遇到 ETXTBSY，也不会留下写了一半的文件。
type FS interface {
	ReadFile(name string) ([]byte, error)
	ReadDir(name string) ([]string, error)
	Stat(name string) (FileInfo, error)
	WriteFile(name string, data []byte, perm fs.FileMode, own *Owner) error
	Mkdir(name string, perm fs.FileMode, own Owner) error
	SetOwnerMode(name string, perm fs.FileMode, own Owner) error
}

// User 是系统用户的 uid 与主组 gid。
type User struct{ UID, GID int }

// ErrNotFound 表示用户或组不存在。
var ErrNotFound = errors.New("不存在")

// Users 查询系统用户与组。
type Users interface {
	Lookup(name string) (User, error)
	InGroup(user, group string) (bool, error)
	// Home 返回用户的家目录。
	Home(name string) (string, error)
}

// Listener 是占用某个 TCP 端口的监听进程；PID 为 0 表示找不到属主进程。
type Listener struct {
	PID int
	Exe string
}

// Ports 查询谁在监听某个 TCP 端口。
type Ports interface {
	Listeners(port int) ([]Listener, error)
}

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
