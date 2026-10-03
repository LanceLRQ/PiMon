package sudocmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNotFound 表示候选目录里没有该命令。
var ErrNotFound = errors.New("未找到命令")

// ErrUnsafe 表示找到了命令，但它或所在目录的属主不是 root，或可被组/他人写入。
var ErrUnsafe = errors.New("命令属主或权限不安全")

// CandidateDirs 是查找命令的系统目录，按优先级排列。不查 PATH，避免用户可写目录混入 sudoers。
var CandidateDirs = []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin", "/usr/local/sbin", "/usr/local/bin"}

// FS 是解析命令路径所需的文件系统操作，测试可注入。
type FS struct {
	Stat         func(string) (fs.FileInfo, error)
	EvalSymlinks func(string) (string, error)
	// Owner 返回文件属主 uid；取不到时第二个返回值为 false（按不安全处理）。
	Owner func(fs.FileInfo) (uint32, bool)
}

// SystemFS 返回真实文件系统实现。
func SystemFS() FS {
	return FS{Stat: os.Stat, EvalSymlinks: filepath.EvalSymlinks, Owner: ownerUID}
}

// Resolve 在 CandidateDirs 里查找命令 name，返回其真实绝对路径（已解析符号链接）。
// 命令文件须是属主 root、可执行、组与他人不可写的普通文件，且从它所在目录到 / 的每一级目录
// 都属主 root、组与他人不可写。不安全的候选会被跳过；全部不可用时返回 ErrUnsafe 或 ErrNotFound。
// 返回的路径同时用于插件调用与 sudoers 规格，两边逐字一致。
func Resolve(fsys FS, name string) (string, error) {
	if !isCommand(name) {
		return "", fmt.Errorf("未知命令 %q", name)
	}
	var unsafeErr error
	for _, dir := range CandidateDirs {
		p := filepath.Join(dir, name)
		if _, err := fsys.Stat(p); err != nil {
			continue
		}
		real, err := fsys.EvalSymlinks(p)
		if err != nil {
			continue
		}
		real = filepath.Clean(real)
		if err := checkSafe(fsys, real); err != nil {
			if unsafeErr == nil {
				unsafeErr = err
			}
			continue
		}
		if err := ValidatePath(real); err != nil {
			if unsafeErr == nil {
				unsafeErr = fmt.Errorf("%w：%v", ErrUnsafe, err)
			}
			continue
		}
		return real, nil
	}
	if unsafeErr != nil {
		return "", unsafeErr
	}
	return "", fmt.Errorf("%w：%s", ErrNotFound, name)
}

// ResolveAll 解析全部命令，返回找到的路径与每个未找到命令的错误。
func ResolveAll(fsys FS) (map[string]string, map[string]error) {
	paths := map[string]string{}
	errs := map[string]error{}
	for _, c := range Commands {
		p, err := Resolve(fsys, c)
		if err != nil {
			errs[c] = err
			continue
		}
		paths[c] = p
	}
	return paths, errs
}

func checkSafe(fsys FS, real string) error {
	fi, err := fsys.Stat(real)
	if err != nil {
		return fmt.Errorf("%w：%s: %v", ErrUnsafe, real, err)
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%w：%s 不是可执行的普通文件", ErrUnsafe, real)
	}
	if err := checkOwner(fsys, real, fi); err != nil {
		return err
	}
	for d := filepath.Dir(real); ; d = filepath.Dir(d) {
		di, err := fsys.Stat(d)
		if err != nil {
			return fmt.Errorf("%w：%s: %v", ErrUnsafe, d, err)
		}
		if !di.IsDir() {
			return fmt.Errorf("%w：%s 不是目录", ErrUnsafe, d)
		}
		if err := checkOwner(fsys, d, di); err != nil {
			return err
		}
		if d == "/" {
			return nil
		}
	}
}

func checkOwner(fsys FS, p string, fi fs.FileInfo) error {
	uid, ok := fsys.Owner(fi)
	if !ok || uid != 0 {
		return fmt.Errorf("%w：%s 的属主不是 root", ErrUnsafe, p)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%w：%s 可被组或他人写入", ErrUnsafe, p)
	}
	return nil
}
