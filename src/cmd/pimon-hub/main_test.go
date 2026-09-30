package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

func runArgs(args ...string) (code int, stdout, stderr string) {
	var o, e bytes.Buffer
	code = run(args, strings.NewReader(""), &o, &e, func(string) string { return "" }, nil)
	return code, o.String(), e.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := runArgs("version")
	if code != 0 || strings.TrimSpace(out) != version.Version {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestHelp(t *testing.T) {
	code, out, _ := runArgs("help")
	if code != 0 || !strings.Contains(out, "serve") || !strings.Contains(out, "restore") || !strings.Contains(out, "plugin") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errOut := runArgs("bogus")
	if code != 2 || !strings.Contains(errOut, "bogus") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	if code, _, _ := runArgs(); code != 2 {
		t.Fatalf("无参数应退出 2，得到 %d", code)
	}
}

func TestRestoreNeedsArchive(t *testing.T) {
	code, _, errOut := runArgs("restore", "--data-dir", t.TempDir())
	if code != 1 && code != 2 {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestCommandFailureExitsOne(t *testing.T) {
	code, _, errOut := runArgs("restore", "--data-dir", t.TempDir(), "/nonexistent/x.tar.gz")
	if code != 1 || errOut == "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestPluginHelpAndUsage(t *testing.T) {
	code, out, _ := runArgs("plugin", "help")
	if code != 0 || !strings.Contains(out, "validate") || !strings.Contains(out, "run") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if code, _, _ := runArgs("plugin"); code != 2 {
		t.Fatalf("无子命令应退出 2，得到 %d", code)
	}
	if code, _, _ := runArgs("plugin", "validate"); code != 2 {
		t.Fatalf("缺目录应退出 2，得到 %d", code)
	}
}

func TestPluginValidateExitCodes(t *testing.T) {
	code, out, _ := runArgs("plugin", "validate", "../../../examples/plugins/shell-disk-load")
	if code != 0 || !strings.Contains(out, "校验通过") {
		t.Fatalf("示例应通过: code=%d out=%q", code, out)
	}
	code, _, errOut := runArgs("plugin", "validate", t.TempDir())
	if code != 1 || errOut == "" {
		t.Fatalf("空目录应退出 1: code=%d err=%q", code, errOut)
	}
}

// manifest 错误只应在输出里出现一次（不再先打明细、再打"错误:"）。
func TestPluginValidateErrorPrintedOnce(t *testing.T) {
	dir := t.TempDir()
	yml := "id: x\nversion: 1.0.0\napi_version: 1\nname: x\nkind: sourcee\nruntime: exec\nruns_on: [hub]\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runArgs("plugin", "validate", dir)
	if code != 1 || strings.Count(out+errOut, "sourcee") != 1 || !strings.Contains(errOut, "第 5 行") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}
