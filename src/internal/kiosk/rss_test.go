package kiosk

import (
	"fmt"
	"path/filepath"
	"testing"
)

func writeFakeProc(t *testing.T, root string, pid, pgrp int, comm string, rssKB int) {
	t.Helper()
	files := map[string]string{
		"stat": fmt.Sprintf("%d (%s) S 1 %d %d 0 -1 4194560\n", pid, comm, pgrp, pgrp),
	}
	if rssKB >= 0 {
		files["status"] = fmt.Sprintf("Name:\t%s\nVmSize:\t  99999 kB\nVmRSS:\t  %d kB\nThreads:\t3\n", comm, rssKB)
	} else {
		files["status"] = "Name:\tkthreadd\nThreads:\t1\n"
	}
	writeFiles(t, filepath.Join(root, fmt.Sprint(pid)), files)
}

func TestSumGroupRSS_只累加同进程组(t *testing.T) {
	root := t.TempDir()
	writeFakeProc(t, root, 100, 100, "chromium", 1000)
	writeFakeProc(t, root, 101, 100, "chromium", 2000)
	writeFakeProc(t, root, 102, 100, "Web Content (x)", 500) // comm 含空格与括号
	writeFakeProc(t, root, 200, 200, "other", 9999)
	writeFakeProc(t, root, 2, 0, "kthreadd", -1)
	writeFiles(t, root, map[string]string{"cpuinfo": "x"}) // 非数字条目忽略
	got, ok := sumGroupRSS(root, 100)
	if !ok || got != (1000+2000+500)*1024 {
		t.Fatalf("got (%d,%v)", got, ok)
	}
}

func TestSumGroupRSS_无进程或无效组为未知(t *testing.T) {
	root := t.TempDir()
	writeFakeProc(t, root, 200, 200, "other", 1)
	if _, ok := sumGroupRSS(root, 100); ok {
		t.Fatal("组内没有进程应为未知")
	}
	if _, ok := sumGroupRSS(root, 0); ok {
		t.Fatal("pgid 无效应为未知")
	}
	if _, ok := sumGroupRSS(filepath.Join(root, "nope"), 100); ok {
		t.Fatal("proc 不存在应为未知")
	}
}

func TestSumGroupRSS_跳过读不到的进程(t *testing.T) {
	root := t.TempDir()
	writeFakeProc(t, root, 100, 100, "chromium", 10)
	// 101 只有 stat 没有 status（进程刚退出）
	writeFiles(t, filepath.Join(root, "101"), map[string]string{"stat": "101 (x) S 1 100 100 0\n"})
	// 102 stat 损坏
	writeFiles(t, filepath.Join(root, "102"), map[string]string{"stat": "garbage"})
	got, ok := sumGroupRSS(root, 100)
	if !ok || got != 10*1024 {
		t.Fatalf("got (%d,%v)", got, ok)
	}
}
