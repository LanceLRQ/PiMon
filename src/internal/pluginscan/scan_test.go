package pluginscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func manifestYAML(id, rt string) string {
	return "id: " + id + "\nversion: 1.0.0\napi_version: 1\nname: " + id + "\nkind: source\nruntime: " + rt +
		"\nruns_on: [hub, agent]\noutputs:\n  - {key: v, type: number, title: V}\n"
}

func put(t *testing.T, root, id, yaml string) string {
	t.Helper()
	d := filepath.Join(root, id)
	for _, err := range []error{
		os.MkdirAll(d, 0o755),
		os.Chmod(d, 0o755),
		os.WriteFile(filepath.Join(d, "plugin.yaml"), []byte(yaml), 0o644),
		os.WriteFile(filepath.Join(d, "run"), []byte("#!/bin/sh\necho '{}'\n"), 0o755),
		os.Chmod(filepath.Join(d, "run"), 0o755),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func issueFor(t *testing.T, issues []Issue, dir string) Issue {
	t.Helper()
	for _, i := range issues {
		if i.Dir == dir {
			return i
		}
	}
	t.Fatalf("没有目录 %s 的加载问题: %+v", dir, issues)
	return Issue{}
}

func TestScanDir合法插件(t *testing.T) {
	root := t.TempDir()
	d := put(t, root, "good", manifestYAML("good", "exec"))
	execs, issues := ScanDir(root, os.Geteuid(), nil, "agent")
	if len(issues) != 0 || len(execs) != 1 {
		t.Fatalf("execs=%v issues=%+v", execs, issues)
	}
	e := execs[0]
	if e.ID != "good" || e.Dir != d || e.RunPath != filepath.Join(d, "run") || e.Manifest == nil {
		t.Fatalf("unexpected exec: %+v", e)
	}
}

func TestScanDir目录不存在无问题(t *testing.T) {
	execs, issues := ScanDir(filepath.Join(t.TempDir(), "nope"), os.Geteuid(), nil, "hub")
	if execs != nil || issues != nil {
		t.Fatalf("execs=%v issues=%v", execs, issues)
	}
}

func TestScanDir非法与冲突(t *testing.T) {
	root := t.TempDir()
	bad := put(t, root, "bad", "not: [valid")
	mis := put(t, root, "mis", manifestYAML("other", "exec"))
	notExec := put(t, root, "ne", manifestYAML("ne", "builtin"))
	conflict := put(t, root, "core", manifestYAML("core", "exec"))
	noRun := put(t, root, "norun", manifestYAML("norun", "exec"))
	must(t, os.Remove(filepath.Join(noRun, "run")))
	noX := put(t, root, "nox", manifestYAML("nox", "exec"))
	must(t, os.Chmod(filepath.Join(noX, "run"), 0o644))
	must(t, os.Mkdir(filepath.Join(root, ".hidden"), 0o755))

	execs, issues := ScanDir(root, os.Geteuid(), map[string]bool{"core": true}, "hub")
	if len(execs) != 0 {
		t.Fatalf("不应有可用插件: %+v", execs)
	}
	want := map[string]IssueKind{
		bad: IssueInvalidManifest, mis: IssueIDMismatch, notExec: IssueNotExec,
		conflict: IssueConflict, noRun: IssueMissingRun, noX: IssueNotExecutable,
	}
	if len(issues) != len(want) {
		t.Fatalf("issues=%+v", issues)
	}
	for dir, kind := range want {
		if got := issueFor(t, issues, dir).Kind; got != kind {
			t.Errorf("%s: kind=%s want %s", dir, got, kind)
		}
	}
}

func TestScanDir属主不符与组可写(t *testing.T) {
	root := t.TempDir()
	d := put(t, root, "x", manifestYAML("x", "exec"))
	// 假定 euid 为一个既不是当前属主也不是 root 的 uid
	other := os.Geteuid() + 12345
	_, issues := ScanDir(root, other, nil, "agent")
	is := issueFor(t, issues, d)
	if is.Kind != IssueInsecure || !strings.Contains(is.Message, "agent 运行用户") {
		t.Fatalf("issue=%+v", is)
	}
	_, issues = ScanDir(root, other, nil, "hub")
	if m := issueFor(t, issues, d).Message; !strings.Contains(m, "hub 运行用户") {
		t.Fatalf("message=%s", m)
	}

	d2 := put(t, root, "y", manifestYAML("y", "exec"))
	must(t, os.Chmod(d2, 0o775))
	_, issues = ScanDir(root, os.Geteuid(), nil, "hub")
	if is := issueFor(t, issues, d2); is.Kind != IssueInsecure || !strings.Contains(is.Message, "g+w") {
		t.Fatalf("issue=%+v", is)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
