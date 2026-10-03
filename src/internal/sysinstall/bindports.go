package sysinstall

import (
	"errors"
	"fmt"
	"net"
	"syscall"
)

// BindPorts 用 net.Listen 试绑定判断端口是否被占用，不解析占用进程（PID 恒为 0），
// 用于没有 /proc 的平台（macOS）。
type BindPorts struct{}

// Listeners 依次在通配地址、127.0.0.1、::1 上试绑定 port，任一返回地址已占用即视为被占，
// 返回一个未知属主的 Listener。BSD 上通配绑定可与只绑回环的监听共存，所以回环必须单独试。
// IPv6 不可用（地址不可用、协议不支持）时忽略该地址。
// 注意：无权限（如非 root 绑定 1024 以下端口）时返回的是错误（errors.Is(err, fs.ErrPermission) 为真），
// 调用方不得把它当作端口被占用的提示。
func (BindPorts) Listeners(port int) ([]Listener, error) {
	for _, host := range []string{"", "127.0.0.1", "::1"} {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
		if err == nil {
			_ = ln.Close()
			continue
		}
		switch {
		case errors.Is(err, syscall.EADDRINUSE):
			return []Listener{{}}, nil
		case host == "::1" && (errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EAFNOSUPPORT)):
			continue
		}
		return nil, fmt.Errorf("试绑定端口 %d: %w", port, err)
	}
	return nil, nil
}

// NewPorts 按平台选择端口查询实现：darwin 试绑定，其余读 /proc。
func NewPorts(goos string) Ports {
	if goos == "darwin" {
		return BindPorts{}
	}
	return ProcPorts{Root: "/proc"}
}
