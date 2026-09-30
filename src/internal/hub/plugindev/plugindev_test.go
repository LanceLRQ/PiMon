package plugindev

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
)

const examplesDir = "../../../../examples/plugins"

var exampleIDs = []string{"shell-disk-load", "python-file-watch"}

// copyExample 把示例插件复制到临时目录（目录名等于 id），保留文件权限。
func copyExample(t *testing.T, id string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), id)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(examplesDir, id))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(examplesDir, id, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		info, _ := e.Info()
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func lineOf(t *testing.T, file, prefix string) int {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, prefix) {
			return i + 1
		}
	}
	t.Fatalf("%s 中找不到以 %q 开头的行", file, prefix)
	return 0
}

func replaceInFile(t *testing.T, file, old, repl string) {
	t.Helper()
	data, _ := os.ReadFile(file)
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s 中没有 %q", file, old)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(data), old, repl, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateExamples(t *testing.T) {
	for _, id := range exampleIDs {
		var out bytes.Buffer
		if err := Validate(copyExample(t, id), &out); err != nil {
			t.Errorf("%s 应通过校验: %v", id, err)
		}
		if !strings.Contains(out.String(), "属主") {
			t.Errorf("%s: 输出应提示 hub 加载时还会检查属主与权限: %q", id, out.String())
		}
	}
}

func TestValidateReportsBrokenLine(t *testing.T) {
	for _, id := range exampleIDs {
		dir := copyExample(t, id)
		manifestPath := filepath.Join(dir, "plugin.yaml")
		replaceInFile(t, manifestPath, "kind: source", "kind: sourcee")
		want := lineOf(t, manifestPath, "kind: sourcee")
		var out bytes.Buffer
		err := Validate(dir, &out)
		var me *manifest.Error
		if !errors.As(err, &me) {
			t.Fatalf("%s: 期望 *manifest.Error，得到 %v", id, err)
		}
		if len(me.Problems) != 1 || me.Problems[0].Line != want {
			t.Errorf("%s: 问题 = %+v，期望在第 %d 行", id, me.Problems, want)
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("第 %d 行", want)) {
			t.Errorf("%s: 错误应带行号: %q", id, err.Error())
		}
	}
}

func TestValidateRunEntry(t *testing.T) {
	dir := copyExample(t, "shell-disk-load")
	if err := os.Chmod(filepath.Join(dir, "run"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Validate(dir, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "run") {
		t.Errorf("run 不可执行应报错: %v", err)
	}
	dir = copyExample(t, "shell-disk-load")
	_ = os.Remove(filepath.Join(dir, "run"))
	if err := Validate(dir, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "run") {
		t.Errorf("缺少 run 应报错: %v", err)
	}
}

func TestValidateDirNameAndRuntime(t *testing.T) {
	dir := copyExample(t, "shell-disk-load")
	renamed := filepath.Join(filepath.Dir(dir), "other-name")
	if err := os.Rename(dir, renamed); err != nil {
		t.Fatal(err)
	}
	if err := Validate(renamed, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "目录名") {
		t.Errorf("目录名与 id 不一致应报错: %v", err)
	}
	dir = copyExample(t, "shell-disk-load")
	replaceInFile(t, filepath.Join(dir, "plugin.yaml"), "runtime: exec", "runtime: builtin")
	if err := Validate(dir, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "exec") {
		t.Errorf("runtime 不是 exec 应报错: %v", err)
	}
}

func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunShellExample(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("没有 sh")
	}
	var out bytes.Buffer
	cfg := writeConfig(t, "cfg.json", `{"path": "/"}`)
	err := Run(context.Background(), copyExample(t, "shell-disk-load"), RunOptions{ConfigPath: cfg}, &out)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	for _, want := range []string{"状态", "disk", "gauge", "load", "number"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("输出缺少 %q:\n%s", want, out.String())
		}
	}
}

func TestRunPythonExampleYAMLConfig(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("没有 python3")
	}
	target := writeConfig(t, "watched.txt", "a\nb\nc\n")
	cfg := writeConfig(t, "cfg.yaml", "path: "+target+"\nmax_bytes: 1000\ntoken: s3cret-token-value\n")
	var out bytes.Buffer
	err := Run(context.Background(), copyExample(t, "python-file-watch"), RunOptions{ConfigPath: cfg}, &out)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"size", "lines", "readable", "6", "3"} {
		if !strings.Contains(s, want) {
			t.Errorf("输出缺少 %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "s3cret-token-value") {
		t.Error("密钥不得出现在输出中")
	}
}

func TestRunConfigValidationFails(t *testing.T) {
	dir := copyExample(t, "python-file-watch")
	cfg := writeConfig(t, "cfg.json", `{}`) // 缺必填 path
	var out bytes.Buffer
	err := Run(context.Background(), dir, RunOptions{ConfigPath: cfg}, &out)
	if err == nil || !strings.Contains(err.Error(), "path") {
		t.Errorf("缺少必填配置应失败并指出字段: %v", err)
	}
	cfg = writeConfig(t, "cfg.txt", `{}`)
	if err := Run(context.Background(), dir, RunOptions{ConfigPath: cfg}, &out); err == nil {
		t.Error("不支持的扩展名应失败")
	}
}

// 插件失败时返回错误，且错误与输出里都不含密钥。
func TestRunPluginFailureRedactsSecret(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "leaky")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := `id: leaky
version: 1.0.0
api_version: 1
name: leaky
kind: source
runtime: exec
runs_on: [hub]
timeout: 5s
config_schema:
  - {key: token, type: secret}
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, "cfg.json", `{"token": "topsecret-123"}`)
	var out bytes.Buffer
	err := Run(context.Background(), dir, RunOptions{ConfigPath: cfg}, &out)
	if err == nil {
		t.Fatal("插件退出码非 0 应失败")
	}
	if strings.Contains(err.Error(), "topsecret-123") || strings.Contains(out.String(), "topsecret-123") {
		t.Errorf("密钥泄露:\nerr=%v\nout=%s", err, out.String())
	}
}

func TestRunBadProxy(t *testing.T) {
	dir := copyExample(t, "shell-disk-load")
	cfg := writeConfig(t, "cfg.json", `{}`)
	if err := Run(context.Background(), dir, RunOptions{ConfigPath: cfg, ProxyURL: "ftp://x:1"}, &bytes.Buffer{}); err == nil {
		t.Error("非法代理应失败")
	}
}

func TestCommandUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	for _, args := range [][]string{{}, {"bogus"}, {"validate"}, {"run"}, {"validate", "a", "b"}} {
		err := Command(context.Background(), args, &out, &errOut)
		var ue UsageError
		if !errors.As(err, &ue) {
			t.Errorf("args=%v 应为用法错误: %v", args, err)
		}
	}
}

func TestCommandFlagsAfterDir(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("没有 sh")
	}
	dir := copyExample(t, "shell-disk-load")
	cfg := writeConfig(t, "cfg.json", `{}`)
	var out bytes.Buffer
	if err := Command(context.Background(), []string{"run", dir, "--config", cfg}, &out, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	out.Reset()
	if err := Command(context.Background(), []string{"validate", dir}, &out, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

// 相对路径的插件目录也要能运行（执行器会切换工作目录）。
func TestRunRelativeDir(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("没有 sh")
	}
	cfg := writeConfig(t, "cfg.json", `{}`)
	var out bytes.Buffer
	if err := Run(context.Background(), filepath.Join(examplesDir, "shell-disk-load"), RunOptions{ConfigPath: cfg}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

func TestValidateRejectsSymlinkRun(t *testing.T) {
	dir := copyExample(t, "shell-disk-load")
	real := filepath.Join(dir, "real-run")
	if err := os.Rename(filepath.Join(dir, "run"), real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real-run", filepath.Join(dir, "run")); err != nil {
		t.Skip("无法创建符号链接")
	}
	err := Validate(dir, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Errorf("符号链接的 run 应报错并说明原因: %v", err)
	}
	if err := Run(context.Background(), dir, RunOptions{}, &bytes.Buffer{}); err == nil {
		t.Error("run 也应拒绝符号链接入口")
	}
}
