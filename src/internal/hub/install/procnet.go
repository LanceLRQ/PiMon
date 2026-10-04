package install

import "github.com/LanceLRQ/PiMon/src/internal/sysinstall"

// ProcPorts 纯 Go 读取 /proc/net/tcp{,6} 与 /proc/*/fd 找出端口的监听进程。
type ProcPorts = sysinstall.ProcPorts
