package sysinstall

import (
	"fmt"
	"net"
)

// BindPorts 用 net.Listen 试绑定判断端口是否被占用，不解析占用进程（PID 恒为 0），
// 用于没有 /proc 的平台（macOS）。
type BindPorts struct{}

// Listeners 在通配地址上试绑定 port：绑定成功说明空闲，返回 nil；地址已被占用返回一个未知属主的 Listener。
func (BindPorts) Listeners(port int) ([]Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		if isAddrInUse(err) {
			return []Listener{{}}, nil
		}
		return nil, fmt.Errorf("试绑定端口 %d: %w", port, err)
	}
	_ = ln.Close()
	return nil, nil
}

// NewPorts 按平台选择端口查询实现：darwin 试绑定，其余读 /proc。
func NewPorts(goos string) Ports {
	if goos == "darwin" {
		return BindPorts{}
	}
	return ProcPorts{Root: "/proc"}
}
