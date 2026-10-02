package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/LanceLRQ/PiMon/src/internal/hub/config"
	"github.com/LanceLRQ/PiMon/src/internal/hub/datalock"
)

// ErrDataDirBusy 表示数据目录已被另一个进程（通常是正在运行的 serve）占用。
var ErrDataDirBusy = errors.New("数据目录已被另一个 pimon-hub 进程占用（服务是否仍在运行？）")

// LockDataDir 确保数据目录存在并对其取独占锁，由 serve 与 restore 使用，
// 在打开数据库之前调用，持有到进程结束。
func LockDataDir(cfg config.Config) (*datalock.Lock, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s: %w", cfg.DataDir, err)
	}
	lk, err := datalock.Acquire(cfg.DataDir)
	if errors.Is(err, datalock.ErrLocked) {
		return nil, fmt.Errorf("%w: %s", ErrDataDirBusy, cfg.DataDir)
	}
	return lk, err
}

// CheckRootRun 拒绝「以 root 运行、而数据目录属主不是 root」的场景：
// 这会让 root 创建的文件使后续以服务用户运行的 serve 打不开数据库。
// 数据目录尚不存在时不拒绝（全新安装由 install 先建目录）。
func CheckRootRun(cfg config.Config) error {
	return checkRoot(cfg, os.Geteuid(), statOwner)
}

func checkRoot(cfg config.Config, euid int, ownerOf func(string) (uint32, error)) error {
	if euid != 0 {
		return nil
	}
	uid, err := ownerOf(cfg.DataDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取数据目录属主: %w", err)
	}
	if uid == 0 {
		return nil
	}
	return fmt.Errorf("数据目录 %s 属于 uid %d，不能以 root 运行本命令；请改用服务用户，例如 sudo -u pimon pimon-hub <命令>",
		cfg.DataDir, uid)
}

// statOwner 返回路径的属主 uid。
func statOwner(path string) (uint32, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("当前平台不支持读取文件属主")
	}
	return st.Uid, nil
}
