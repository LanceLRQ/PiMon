package install

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

type execRunner struct{}

func (r execRunner) Run(ctx context.Context, argv ...string) (string, error) {
	return r.run(ctx, nil, false, argv)
}

func (r execRunner) RunEnv(ctx context.Context, setEnv []string, argv ...string) (string, error) {
	return r.run(ctx, setEnv, true, argv)
}

func (execRunner) run(ctx context.Context, setEnv []string, own bool, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if own {
		cmd.Env = buildEnv(os.Environ(), setEnv)
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

type osFS struct{}

func (osFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

func (osFS) ReadDir(name string) ([]string, error) {
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

func (osFS) Stat(name string) (FileInfo, error) { return statInfo(os.Stat(name)) }

func (osFS) Lstat(name string) (FileInfo, error) { return statInfo(os.Lstat(name)) }

func statInfo(fi os.FileInfo, err error) (FileInfo, error) {
	if err != nil {
		return FileInfo{}, err
	}
	out := FileInfo{IsDir: fi.IsDir(), IsSymlink: fi.Mode()&os.ModeSymlink != 0, Mode: fi.Mode().Perm(), ModTime: fi.ModTime(), UID: -1, GID: -1}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		out.UID, out.GID = int(st.Uid), int(st.Gid)
	}
	return out, nil
}

func (osFS) WriteFile(name string, data []byte, perm fs.FileMode, own *Owner) (err error) {
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

func (o osFS) Mkdir(name string, perm fs.FileMode, own Owner) error {
	if err := os.MkdirAll(name, perm); err != nil {
		return err
	}
	return o.SetOwnerMode(name, perm, own)
}

func (osFS) SetOwnerMode(name string, perm fs.FileMode, own Owner) error {
	if err := os.Chown(name, own.UID, own.GID); err != nil {
		return err
	}
	return os.Chmod(name, perm)
}

type osUsers struct{}

func (osUsers) Lookup(name string) (User, error) {
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

func (osUsers) Home(name string) (string, error) {
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

func (osUsers) InGroup(name, group string) (bool, error) {
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

// httpHealth 探测服务是否就绪：先明文 GET，失败（连接错误或非 2xx，含 TLS 端口对明文请求回的 400）
// 后，对同一端口再用 https 探一次（自签名证书，只访问回环、只看状态码）；任一次 2xx 即就绪。
func httpHealth(ctx context.Context, url string) error {
	plainErr := probeHealth(ctx, http.DefaultClient, url)
	if plainErr == nil {
		return nil
	}
	rest, ok := strings.CutPrefix(url, "http://")
	if !ok {
		return plainErr
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // 只探测回环地址，仅看状态码
	}}
	defer client.CloseIdleConnections()
	if err := probeHealth(ctx, client, "https://"+rest); err != nil {
		return fmt.Errorf("http: %v；https: %w", plainErr, err)
	}
	return nil
}

// probeHealth 对地址做一次 GET，2xx 视为就绪。
func probeHealth(ctx context.Context, client *http.Client, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// buildEnv 返回子进程环境：去掉 base 里全部 PIMON_* 再追加 set。
func buildEnv(base, set []string) []string {
	out := make([]string, 0, len(base)+len(set))
	for _, kv := range base {
		if !strings.HasPrefix(kv, "PIMON_") {
			out = append(out, kv)
		}
	}
	return append(out, set...)
}
