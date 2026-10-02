package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
)

// lightdm 配置按先后顺序读取，后读到的覆盖先读到的；主配置文件最后读。
var lightdmConfDirs = []string{
	"/usr/share/lightdm/lightdm.conf.d",
	"/etc/xdg/lightdm/lightdm.conf.d",
	"/etc/lightdm/lightdm.conf.d",
}

const lightdmMainConf = "/etc/lightdm/lightdm.conf"

// detectAutologinUser 返回 lightdm 配置里 [Seat:*] 段的 autologin-user；没有配置返回空串。
func detectAutologinUser(fsys FS) (string, error) {
	var files []string
	for _, dir := range lightdmConfDirs {
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
	files = append(files, lightdmMainConf)
	user := ""
	for _, f := range files {
		b, err := fsys.ReadFile(f)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("读取 %s: %w", f, err)
		}
		if v, ok := seatAutologinUser(string(b)); ok {
			user = v
		}
	}
	return user, nil
}

// seatAutologinUser 解析一份 lightdm 配置，返回 [Seat:*] 段内最后一个 autologin-user（跳过注释行）。
func seatAutologinUser(content string) (string, bool) {
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

func (r *run) addDesktopUser(ctx context.Context) error {
	name := r.opts.DesktopUser
	explicit := name != ""
	if !explicit {
		var err error
		if name, err = detectAutologinUser(r.FS); err != nil {
			return err
		}
	}
	if name == "" {
		r.rep.skipped("桌面用户加入 "+serviceUser+" 组", "未指定 --desktop-user，也没有检测到 lightdm 自动登录用户；kiosk 要读取屏幕令牌时请手动执行 usermod -aG "+serviceUser+" <桌面用户>")
		return nil
	}
	if _, err := r.Users.Lookup(name); err != nil {
		if errors.Is(err, ErrNotFound) && !explicit {
			r.rep.skipped("桌面用户加入 "+serviceUser+" 组", fmt.Sprintf("lightdm 自动登录用户 %s 在系统里不存在", name))
			return nil
		}
		return fmt.Errorf("桌面用户 %s: %w", name, err)
	}
	in, err := r.Users.InGroup(name, serviceUser)
	if err != nil {
		return fmt.Errorf("查询用户 %s 的组: %w", name, err)
	}
	if in {
		r.rep.existed(fmt.Sprintf("桌面用户 %s 在 %s 组", name, serviceUser), "")
		return nil
	}
	if _, err := r.Runner.Run(ctx, cmdUsermod, "-aG", serviceUser, name); err != nil {
		return fmt.Errorf("把 %s 加入 %s 组: %w", name, serviceUser, err)
	}
	r.needRelogin = name
	r.rep.done(fmt.Sprintf("桌面用户 %s 加入 %s 组", name, serviceUser), "需重新登录后生效")
	return nil
}
