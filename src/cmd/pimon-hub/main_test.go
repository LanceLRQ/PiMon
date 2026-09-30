package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

func runArgs(args ...string) (code int, stdout, stderr string) {
	var o, e bytes.Buffer
	code = run(args, strings.NewReader(""), &o, &e, func(string) string { return "" })
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
	if code != 0 || !strings.Contains(out, "serve") || !strings.Contains(out, "restore") {
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
