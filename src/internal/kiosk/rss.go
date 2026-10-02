package kiosk

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sumGroupRSS 对 procRoot 下进程组号为 pgid 的所有进程，累加 <pid>/status 里的 VmRSS，返回字节数。
// pgid 无效、proc 读不到或组内没有进程时返回 false（未知，而不是 0）。
func sumGroupRSS(procRoot string, pgid int) (int64, bool) {
	if pgid <= 0 {
		return 0, false
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, false
	}
	var total int64
	found := false
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		dir := filepath.Join(procRoot, e.Name())
		if g, ok := readPgrp(filepath.Join(dir, "stat")); !ok || g != pgid {
			continue
		}
		kb, ok := readVmRSSKB(filepath.Join(dir, "status"))
		if !ok {
			continue // 进程刚退出或是内核线程
		}
		found = true
		total += kb * 1024
	}
	return total, found
}

// readPgrp 解析 /proc/<pid>/stat 的进程组号（第 5 个字段）；comm 可能含空格与括号，从最后一个 ) 之后切。
func readPgrp(statPath string) (int, bool) {
	data, err := os.ReadFile(statPath)
	if err != nil {
		return 0, false
	}
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, false
	}
	// ) 之后依次是 state ppid pgrp ...
	fields := strings.Fields(s[i+1:])
	if len(fields) < 3 {
		return 0, false
	}
	g, err := strconv.Atoi(fields[2])
	return g, err == nil
}

func readVmRSSKB(statusPath string) (int64, bool) {
	f, err := os.Open(statusPath)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		rest, ok := strings.CutPrefix(sc.Text(), "VmRSS:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return 0, false
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		return kb, err == nil
	}
	return 0, false
}
