package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

func runArgs(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errb, func(string) string { return "" })
	return code, out.String(), errb.String()
}

func TestVersion打印版本号(t *testing.T) {
	code, out, _ := runArgs("version")
	if code != 0 || strings.TrimSpace(out) != version.Version {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestSubcommands未实现返回1(t *testing.T) {
	for _, cmd := range []string{"install", "upgrade", "uninstall"} {
		code, out, errb := runArgs(cmd)
		if code != 1 || out != "" || !strings.Contains(errb, "未实现") || strings.Count(strings.TrimSpace(errb), "\n") != 0 {
			t.Fatalf("%s: code=%d out=%q err=%q", cmd, code, out, errb)
		}
	}
}

func TestUnknownCommand用法退出码2(t *testing.T) {
	code, _, errb := runArgs("bogus")
	if code != 2 || !strings.Contains(errb, "未知命令") {
		t.Fatalf("code=%d err=%q", code, errb)
	}
	for _, cmd := range []string{"install", "pair", "run", "status", "repin", "upgrade", "uninstall", "version"} {
		if !strings.Contains(errb, cmd) {
			t.Errorf("用法缺少 %s", cmd)
		}
	}
}

func TestNoArgs用法退出码2(t *testing.T) {
	code, _, errb := runArgs()
	if code != 2 || !strings.Contains(errb, "用法") {
		t.Fatalf("code=%d err=%q", code, errb)
	}
}

func TestHelp退出码0(t *testing.T) {
	code, out, _ := runArgs("help")
	if code != 0 || !strings.Contains(out, "用法") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}
