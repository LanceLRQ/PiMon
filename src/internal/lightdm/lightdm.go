// Package lightdm 解析 lightdm 配置里的自动登录用户，install 与会话看门狗共用。
package lightdm

import (
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
)

// Files 是读取配置所需的最小文件系统接口。
type Files interface {
	ReadFile(name string) ([]byte, error)
	ReadDir(name string) ([]string, error)
}

// confDirs 按先后顺序读取，后读到的覆盖先读到的；主配置文件最后读。
var confDirs = []string{
	"/usr/share/lightdm/lightdm.conf.d",
	"/etc/xdg/lightdm/lightdm.conf.d",
	"/etc/lightdm/lightdm.conf.d",
}

// MainConf 是 lightdm 主配置文件路径。
const MainConf = "/etc/lightdm/lightdm.conf"

// AutologinUser 返回 lightdm 配置里 [Seat:*] 段的 autologin-user；没有配置返回空串。
func AutologinUser(fsys Files) (string, error) {
	var files []string
	for _, dir := range confDirs {
		names, err := fsys.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("读取 %s: %w", dir, err)
		}
		slices.Sort(names)
		for _, n := range names {
			if strings.HasSuffix(n, ".conf") {
				files = append(files, path.Join(dir, n))
			}
		}
	}
	files = append(files, MainConf)
	user := ""
	for _, f := range files {
		b, err := fsys.ReadFile(f)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("读取 %s: %w", f, err)
		}
		if v, ok := SeatAutologinUser(string(b)); ok {
			user = v
		}
	}
	return user, nil
}

// SeatAutologinUser 解析一份 lightdm 配置，返回 [Seat:*] 段内最后一个 autologin-user（跳过注释行）。
func SeatAutologinUser(content string) (string, bool) {
	in, found, val := false, false, ""
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			in = line == "[Seat:*]"
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == "autologin-user" {
			val, found = strings.TrimSpace(v), true
		}
	}
	return val, found
}
