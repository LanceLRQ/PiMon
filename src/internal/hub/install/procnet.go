package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcPorts 纯 Go 读取 /proc/net/tcp{,6} 与 /proc/*/fd 找出端口的监听进程，Root 可注入以便测试。
type ProcPorts struct{ Root string }

// Listeners 返回监听 port 的进程（按 PID 去重；找不到属主进程时 PID 为 0）。
func (p ProcPorts) Listeners(port int) ([]Listener, error) {
	inodes := map[string]bool{}
	for i, name := range []string{"net/tcp", "net/tcp6"} {
		b, err := os.ReadFile(filepath.Join(p.Root, name))
		if err != nil {
			// 没有 IPv6 的系统没有 tcp6；tcp 缺失才是真问题。
			if i == 1 && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("读取 /proc/%s: %w", name, err)
		}
		for _, ino := range listenInodes(string(b), port) {
			inodes[ino] = true
		}
	}
	if len(inodes) == 0 {
		return nil, nil
	}
	ents, err := os.ReadDir(p.Root)
	if err != nil {
		return nil, err
	}
	var out []Listener
	owned := map[string]bool{}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join(p.Root, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			ino := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			if !inodes[ino] {
				continue
			}
			owned[ino] = true
			exe, _ := os.Readlink(filepath.Join(p.Root, e.Name(), "exe"))
			out = append(out, Listener{PID: pid, Exe: exe})
			break
		}
	}
	if len(owned) < len(inodes) && len(out) == 0 {
		out = append(out, Listener{})
	}
	return out, nil
}

// listenInodes 解析 /proc/net/tcp 风格文本，返回本地端口为 port 且处于 LISTEN(0A) 的 socket inode。
func listenInodes(content string, port int) []string {
	var out []string
	want := fmt.Sprintf("%04X", port)
	for i, line := range strings.Split(content, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 10 || f[3] != "0A" {
			continue
		}
		idx := strings.LastIndexByte(f[1], ':')
		if idx < 0 || !strings.EqualFold(f[1][idx+1:], want) {
			continue
		}
		out = append(out, f[9])
	}
	return out
}
