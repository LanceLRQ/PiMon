package sysinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// ExecRunner 是 Runner 的真实实现，用 os/exec 执行命令；非零退出返回 *ExitError。
type ExecRunner struct{}

func (r ExecRunner) Run(ctx context.Context, argv ...string) (string, error) {
	return r.run(ctx, nil, false, argv)
}

func (r ExecRunner) RunEnv(ctx context.Context, setEnv []string, argv ...string) (string, error) {
	return r.run(ctx, setEnv, true, argv)
}

func (ExecRunner) run(ctx context.Context, setEnv []string, own bool, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if own {
		cmd.Env = BuildEnv(os.Environ(), setEnv)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out.String(), &ExitError{Argv: argv, Code: ee.ExitCode(), Stderr: errBuf.String()}
		}
		return out.String(), fmt.Errorf("执行 %s: %w", argv[0], err)
	}
	return out.String(), nil
}

// OSFS 是 FS 的真实实现；WriteFile 先写同目录临时文件再 rename，保证原子。
type OSFS struct{}

func (OSFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

func (OSFS) ReadDir(name string) ([]string, error) {
	ents, err := os.ReadDir(name)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names, nil
}

func (OSFS) Stat(name string) (FileInfo, error) { return statInfo(os.Stat(name)) }

func (OSFS) Lstat(name string) (FileInfo, error) { return statInfo(os.Lstat(name)) }

func statInfo(fi os.FileInfo, err error) (FileInfo, error) {
	if err != nil {
		return FileInfo{}, err
	}
	out := FileInfo{IsDir: fi.IsDir(), IsSymlink: fi.Mode()&os.ModeSymlink != 0, IsRegular: fi.Mode().IsRegular(), Mode: fi.Mode().Perm(), ModTime: fi.ModTime(), UID: -1, GID: -1}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		out.UID, out.GID = int(st.Uid), int(st.Gid)
	}
	return out, nil
}

func (OSFS) WriteFile(name string, data []byte, perm fs.FileMode, own *Owner) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(name), ".pimon-install-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if own != nil {
		if err = tmp.Chown(own.UID, own.GID); err != nil {
			return err
		}
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}

func (o OSFS) Mkdir(name string, perm fs.FileMode, own Owner) error {
	if err := os.MkdirAll(name, perm); err != nil {
		return err
	}
	return o.SetOwnerMode(name, perm, own)
}

func (OSFS) SetOwnerMode(name string, perm fs.FileMode, own Owner) error {
	if err := os.Chown(name, own.UID, own.GID); err != nil {
		return err
	}
	return os.Chmod(name, perm)
}

// OSUsers 是 Users 的真实实现，基于 os/user；用户不存在时返回 ErrNotFound。
type OSUsers struct{}

func (OSUsers) Lookup(name string) (User, error) {
	u, err := user.Lookup(name)
	if err != nil {
		var unk user.UnknownUserError
		if errors.As(err, &unk) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	var out User
	if _, err := fmt.Sscan(u.Uid, &out.UID); err != nil {
		return User{}, fmt.Errorf("解析 uid %q: %w", u.Uid, err)
	}
	if _, err := fmt.Sscan(u.Gid, &out.GID); err != nil {
		return User{}, fmt.Errorf("解析 gid %q: %w", u.Gid, err)
	}
	return out, nil
}

func (OSUsers) Home(name string) (string, error) {
	u, err := user.Lookup(name)
	if err != nil {
		var unk user.UnknownUserError
		if errors.As(err, &unk) {
			return "", ErrNotFound
		}
		return "", err
	}
	return u.HomeDir, nil
}

func (OSUsers) InGroup(name, group string) (bool, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return false, err
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		var unk user.UnknownGroupError
		if errors.As(err, &unk) {
			return false, nil
		}
		return false, err
	}
	gids, err := u.GroupIds()
	if err != nil {
		return false, err
	}
	return slices.Contains(gids, g.Gid), nil
}

// BuildEnv 返回子进程环境：去掉 base 里全部 PIMON_* 再追加 set。
func BuildEnv(base, set []string) []string {
	out := make([]string, 0, len(base)+len(set))
	for _, kv := range base {
		if !strings.HasPrefix(kv, "PIMON_") {
			out = append(out, kv)
		}
	}
	return append(out, set...)
}
