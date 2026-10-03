package config

import (
	"os"
	"path/filepath"
)

// Env 是路径解析的输入，测试只构造它，不读真实环境。
type Env struct {
	GOOS   string
	EUID   int
	Home   string
	Getenv func(string) string
	// User 对应 --user：Linux 上强制用户模式。
	User bool
}

// UserMode 报告是否使用用户模式：macOS 总是用户模式，Linux 仅在 --user 时。
func (e Env) UserMode() bool { return e.GOOS == "darwin" || e.User }

// Paths 是一种运行模式下的全部固定路径。
type Paths struct {
	Bin       string
	Config    string
	DataDir   string
	PluginDir string
	DB        string
	CertFile  string
	KeyFile   string
	CtlSock   string
	// LogFile 只在用户模式（macOS launchd）使用；系统模式日志走 journal，为空。
	LogFile string
	System  bool
}

// DataDirMode 返回数据目录应有的权限：用户模式 0700，系统模式 0750。
func (p Paths) DataDirMode() os.FileMode {
	if p.System {
		return 0o750
	}
	return 0o700
}

const (
	systemBin  = "/usr/local/bin/pimon-agent"
	systemConf = "/etc/pimon-agent/agent.json"
	systemData = "/var/lib/pimon-agent"
	systemSock = "/run/pimon-agent/ctl.sock"
)

// Resolve 按 env 解析路径；用户模式缺少家目录时返回 ErrNoHome。
func Resolve(env Env) (Paths, error) {
	if !env.UserMode() {
		return Paths{
			Bin: systemBin, Config: systemConf, DataDir: systemData,
			PluginDir: filepath.Join(systemData, "plugins"),
			DB:        filepath.Join(systemData, "agent.db"),
			CertFile:  filepath.Join(systemData, "agent.crt"),
			KeyFile:   filepath.Join(systemData, "agent.key"),
			CtlSock:   systemSock, System: true,
		}, nil
	}
	if env.Home == "" {
		return Paths{}, ErrNoHome
	}
	d := filepath.Join(env.Home, ".pimon")
	return Paths{
		Bin: filepath.Join(d, "bin", "pimon-agent"), Config: filepath.Join(d, "agent.json"),
		DataDir: d, PluginDir: filepath.Join(d, "plugins"), DB: filepath.Join(d, "agent.db"),
		CertFile: filepath.Join(d, "agent.crt"), KeyFile: filepath.Join(d, "agent.key"),
		CtlSock: filepath.Join(d, "agent.sock"), LogFile: filepath.Join(d, "agent.log"),
	}, nil
}

// EnsureDataDir 创建数据目录（用户模式 0700，系统模式 0750）；已存在时不改权限。
func EnsureDataDir(p Paths) error {
	return os.MkdirAll(p.DataDir, p.DataDirMode())
}
