package upgrade

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// tempPattern 是下载临时文件名模式；与目标二进制同目录，保证 rename 是同一文件系统内的原子替换。
const tempPattern = ".pimon-agent-upgrade-*"

// tempFile 是写完的下载临时文件（0600，仍打开），Sum 是边写边算的 SHA-256。
type tempFile struct {
	f   *os.File
	Sum string
}

// discard 关闭并删除临时文件。
func (t *tempFile) discard() {
	_ = t.f.Close()
	_ = os.Remove(t.f.Name())
}

// commit 把临时文件替换到 target：fsync → chmod 0755 → close → rename。不原地写目标
// （macOS 上原地改写正在运行的已签名二进制会被内核杀掉）；任一步失败删除临时文件，target 保持原样。
func (t *tempFile) commit(target string) (err error) {
	defer func() {
		if err != nil {
			t.discard()
		}
	}()
	if err := t.f.Sync(); err != nil {
		return fmt.Errorf("写入临时文件: %w", err)
	}
	if err := t.f.Chmod(0o755); err != nil {
		return fmt.Errorf("设置临时文件权限: %w", err)
	}
	if err := t.f.Close(); err != nil {
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	if err := os.Rename(t.f.Name(), target); err != nil {
		return fmt.Errorf("替换 %s: %w", target, err)
	}
	return nil
}

// checkTarget 确认要替换的二进制存在且是普通文件（不跟随符号链接，避免把链接替换成文件或写到别处）。
func checkTarget(target string) error {
	fi, err := os.Lstat(target)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("找不到已安装的二进制 %s，请先执行 pimon-agent install", target)
	case err != nil:
		return fmt.Errorf("检查 %s: %w", target, err)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%s 不是普通文件（可能是符号链接），拒绝替换", target)
	}
	if !filepath.IsAbs(target) {
		return fmt.Errorf("二进制路径 %s 不是绝对路径", target)
	}
	return nil
}
