// Package config 解析中枢的启动配置：监听地址、数据目录、日志级别。
// 其余配置一律存数据库并在网页里管理，不在此增加参数。
package config

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
)

const (
	defaultAddr     = ":31415"
	defaultDataDir  = "/var/lib/pimon"
	defaultLogLevel = "info"
)

// Config 是中枢启动配置。
type Config struct {
	Addr     string // HTTP(S) 监听地址
	DataDir  string // 数据目录
	LogLevel string // debug | info | warn | error
}

// Parse 按「命令行参数 > 环境变量 > 默认值」的优先级解析配置。
// getenv 由调用方注入（通常为 os.Getenv）；返回值中的 rest 为命令行剩余的位置参数。
func Parse(args []string, getenv func(string) string) (cfg Config, rest []string, err error) {
	cfg = Config{
		Addr:     pick(getenv("PIMON_ADDR"), defaultAddr),
		DataDir:  pick(getenv("PIMON_DATA_DIR"), defaultDataDir),
		LogLevel: pick(getenv("PIMON_LOG_LEVEL"), defaultLogLevel),
	}

	fs := flag.NewFlagSet("pimon-hub", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "监听地址")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "数据目录")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "日志级别：debug|info|warn|error")
	if err = fs.Parse(args); err != nil {
		return Config{}, nil, fmt.Errorf("解析命令行参数: %w", err)
	}

	if _, err = parseLevel(cfg.LogLevel); err != nil {
		return Config{}, nil, err
	}
	return cfg, fs.Args(), nil
}

func pick(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func parseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("非法日志级别 %q，可选 debug|info|warn|error", s)
}

// SlogLevel 返回对应的 slog 级别；Parse 已校验过，非法值按 info 处理。
func (c Config) SlogLevel() slog.Level {
	l, err := parseLevel(c.LogLevel)
	if err != nil {
		return slog.LevelInfo
	}
	return l
}

// DBPath 是 SQLite 数据库文件。
func (c Config) DBPath() string { return filepath.Join(c.DataDir, "pimon.db") }

// SecretKeyPath 是加密主密钥文件。
func (c Config) SecretKeyPath() string { return filepath.Join(c.DataDir, "secret.key") }

// ScreenTokenPath 是屏幕令牌文件。
func (c Config) ScreenTokenPath() string { return filepath.Join(c.DataDir, "screen.token") }

// BackupDir 是备份目录。
func (c Config) BackupDir() string { return filepath.Join(c.DataDir, "backups") }

// PluginDir 是 exec 插件目录，每个插件一个子目录。
func (c Config) PluginDir() string { return filepath.Join(c.DataDir, "plugins") }

// CertPath 是自签名证书文件。
func (c Config) CertPath() string { return filepath.Join(c.DataDir, "hub.crt") }

// KeyPath 是自签名证书私钥文件。
func (c Config) KeyPath() string { return filepath.Join(c.DataDir, "hub.key") }
