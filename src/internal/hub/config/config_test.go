package config

import (
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseDefaults(t *testing.T) {
	cfg, rest, err := Parse(nil, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":31415" || cfg.DataDir != "/var/lib/pimon" || cfg.LogLevel != "info" {
		t.Fatalf("默认值不符: %+v", cfg)
	}
	if len(rest) != 0 {
		t.Fatalf("不应有位置参数: %v", rest)
	}
}

func TestParseEnvOverridesDefault(t *testing.T) {
	cfg, _, err := Parse(nil, env(map[string]string{
		"PIMON_ADDR": "127.0.0.1:9000", "PIMON_DATA_DIR": "/tmp/d", "PIMON_LOG_LEVEL": "debug",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:9000" || cfg.DataDir != "/tmp/d" || cfg.LogLevel != "debug" {
		t.Fatalf("环境变量未生效: %+v", cfg)
	}
}

func TestParseFlagOverridesEnv(t *testing.T) {
	cfg, _, err := Parse(
		[]string{"--addr", ":1", "--data-dir", "/x", "--log-level", "warn"},
		env(map[string]string{"PIMON_ADDR": ":2", "PIMON_DATA_DIR": "/y", "PIMON_LOG_LEVEL": "debug"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":1" || cfg.DataDir != "/x" || cfg.LogLevel != "warn" {
		t.Fatalf("命令行参数未优先: %+v", cfg)
	}
}

func TestParseFlagPartialKeepsEnv(t *testing.T) {
	cfg, _, err := Parse([]string{"--addr", ":1"}, env(map[string]string{"PIMON_DATA_DIR": "/y"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":1" || cfg.DataDir != "/y" {
		t.Fatalf("未指定的参数应回落到环境变量: %+v", cfg)
	}
}

func TestParsePositionalArgs(t *testing.T) {
	_, rest, err := Parse([]string{"--data-dir", "/x", "backup.tar.gz"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rest, []string{"backup.tar.gz"}) {
		t.Fatalf("位置参数不符: %v", rest)
	}
}

func TestParseInvalidLogLevel(t *testing.T) {
	if _, _, err := Parse([]string{"--log-level", "loud"}, env(nil)); err == nil {
		t.Fatal("命令行非法日志级别应报错")
	}
	if _, _, err := Parse(nil, env(map[string]string{"PIMON_LOG_LEVEL": "loud"})); err == nil {
		t.Fatal("环境变量非法日志级别应报错")
	}
}

func TestParseUnknownFlag(t *testing.T) {
	if _, _, err := Parse([]string{"--nope"}, env(nil)); err == nil {
		t.Fatal("未知参数应报错")
	}
}

func TestSlogLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo,
		"warn": slog.LevelWarn, "error": slog.LevelError,
	}
	for name, want := range cases {
		cfg, _, err := Parse([]string{"--log-level", name}, env(nil))
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.SlogLevel(); got != want {
			t.Errorf("%s: got %v want %v", name, got, want)
		}
	}
}

func TestDerivedPaths(t *testing.T) {
	cfg := Config{DataDir: "/data"}
	checks := map[string]string{
		cfg.DBPath():          filepath.Join("/data", "pimon.db"),
		cfg.SecretKeyPath():   filepath.Join("/data", "secret.key"),
		cfg.ScreenTokenPath(): filepath.Join("/data", "screen.token"),
		cfg.BackupDir():       filepath.Join("/data", "backups"),
		cfg.PluginDir():       filepath.Join("/data", "plugins"),
		cfg.CertPath():        filepath.Join("/data", "hub.crt"),
		cfg.KeyPath():         filepath.Join("/data", "hub.key"),
	}
	for got, want := range checks {
		if got != want {
			t.Errorf("got %s want %s", got, want)
		}
	}
}

// 相对数据目录在解析时转成绝对路径（exec 插件的工作目录是插件目录，相对路径会失效）。
func TestParseRelativeDataDirMadeAbsolute(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg, _, err := Parse([]string{"--data-dir", "data"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	wd, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wd, "data"); cfg.DataDir != want || !filepath.IsAbs(cfg.PluginDir()) {
		t.Fatalf("DataDir = %q，期望 %q", cfg.DataDir, want)
	}
}
