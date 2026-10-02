package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/LanceLRQ/PiMon/src/internal/labwc"
	"github.com/LanceLRQ/PiMon/src/internal/sessionwd"
)

const (
	kioskAutostartLine = "/usr/bin/systemd-cat -t pimon-kiosk /usr/local/bin/pimon-hub kiosk --hub http://127.0.0.1:31415 &"
	greeterAutostart   = "/etc/xdg/labwc-greeter/autostart"
	systemAutostart    = "/etc/xdg/labwc/autostart"
	backupTimeLayout   = "20060102T150405Z"
)

// resolveKioskUser 在任何改动之前确定 kiosk 的桌面用户，找不到就直接报错，避免装一半。
func (r *run) resolveKioskUser(_ context.Context) error {
	if !r.opts.Kiosk {
		return nil
	}
	name := r.opts.DesktopUser
	if name == "" {
		var err error
		if name, err = detectAutologinUser(r.FS); err != nil {
			return err
		}
	}
	if name == "" {
		return errors.New("--kiosk 需要桌面用户：请用 --desktop-user 指定，或在 lightdm 配置里设置 autologin-user")
	}
	if !sessionwd.UserNamePattern.MatchString(name) {
		return fmt.Errorf("桌面用户名 %q 含有不允许的字符", name)
	}
	u, err := r.Users.Lookup(name)
	if err != nil {
		return fmt.Errorf("桌面用户 %s: %w", name, err)
	}
	home, err := r.Users.Home(name)
	if err != nil {
		return fmt.Errorf("查询桌面用户 %s 的家目录: %w", name, err)
	}
	home = path.Clean(home)
	if fi, err := r.FS.Stat(home); err != nil || !fi.IsDir {
		return fmt.Errorf("桌面用户 %s 的家目录 %s 不存在", name, home)
	}
	r.kioskUser, r.kioskHome, r.kioskOwner = name, home, Owner(u)
	return r.preflightHomePaths()
}

// kioskHomePaths 是 --kiosk 会读写的家目录内路径（相对家目录），自上而下排列。
func kioskHomePaths() []string {
	theme := ".icons/" + cursorThemeName
	paths := []string{".config", ".config/labwc", ".config/labwc/autostart", ".config/labwc/environment",
		".icons", theme, theme + "/index.theme", theme + "/cursors"}
	for _, n := range cursorNames {
		paths = append(paths, theme+"/cursors/"+n)
	}
	return paths
}

// preflightHomePaths 在任何改动之前确认这些路径上没有符号链接：
// root 写家目录时跟随链接会把文件落到别处（并被 chown 给桌面用户）。
func (r *run) preflightHomePaths() error {
	for _, rel := range kioskHomePaths() {
		if err := r.noSymlink(path.Join(r.kioskHome, rel)); err != nil {
			return err
		}
	}
	return nil
}

// noSymlink 拒绝符号链接；路径不存在视为通过。
func (r *run) noSymlink(p string) error {
	fi, err := r.FS.Lstat(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("检查 %s: %w", p, err)
	case fi.IsSymlink:
		return fmt.Errorf("%s 是符号链接，拒绝以 root 身份写入桌面用户目录；请先把它换成普通文件或目录", p)
	}
	return nil
}

// installKiosk 安装桌面会话里的 kiosk 配置，放在其他步骤之后执行。
func (r *run) installKiosk(_ context.Context) error {
	if !r.opts.Kiosk {
		return nil
	}
	if err := r.ensureUserAutostart(); err != nil {
		return err
	}
	if err := r.removeGreeterSwayidle(); err != nil {
		return err
	}
	if err := r.checkSystemAutostart(); err != nil {
		return err
	}
	if err := r.installCursorTheme(); err != nil {
		return err
	}
	if err := r.setCursorEnv(); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(r.Out, "kiosk 配置在 %s 下次登录桌面后生效；可执行 sudo systemctl restart lightdm（会结束当前桌面会话）或重启树莓派。\n", r.kioskUser)
	return nil
}

func (r *run) ensureUserAutostart() error {
	file := path.Join(r.kioskHome, ".config/labwc/autostart")
	var addedLine bool
	var removed []string
	var final string
	_, existed, err := r.editText(file, true, func(old string) string {
		for _, l := range strings.Split(old, "\n") {
			if labwc.IsSwayidleLine(l) {
				removed = append(removed, strings.TrimSpace(l))
			}
		}
		out, _ := labwc.RemoveSwayidle(old)
		out, addedLine = labwc.EnsureLine(out, kioskAutostartLine)
		final = out
		return out
	})
	if err != nil {
		return err
	}
	switch {
	case addedLine && !existed:
		r.rep.done("kiosk 启动行 "+file, "新建文件，属主 "+r.kioskUser)
	case addedLine:
		r.rep.done("kiosk 启动行 "+file, "已追加")
	default:
		r.rep.existed("kiosk 启动行 "+file, "")
	}
	if len(removed) > 0 {
		r.rep.done("关闭系统空闲息屏（删 swayidle 行）"+file, "")
		r.printRemoved(removed)
	} else {
		r.rep.existed("用户 autostart 无 swayidle 行", file)
	}
	for _, l := range strings.Split(final, "\n") {
		t := strings.TrimSpace(l)
		low := strings.ToLower(t)
		if t == "" || t == kioskAutostartLine || labwc.IsComment(l) {
			continue
		}
		if strings.Contains(low, "kiosk") || strings.Contains(low, "chromium") {
			r.rep.warn("autostart 里还有疑似 kiosk 的启动行，请人工确认是否清理", t)
		}
	}
	return nil
}

func (r *run) removeGreeterSwayidle() error {
	var removed []string
	_, existed, err := r.editText(greeterAutostart, false, func(old string) string {
		for _, l := range strings.Split(old, "\n") {
			if labwc.IsSwayidleLine(l) {
				removed = append(removed, strings.TrimSpace(l))
			}
		}
		out, _ := labwc.RemoveSwayidle(old)
		return out
	})
	switch {
	case err != nil:
		return err
	case !existed:
		r.rep.skipped("greeter autostart", greeterAutostart+" 不存在")
	case len(removed) > 0:
		r.rep.done("关闭登录界面空闲息屏（删 swayidle 行）"+greeterAutostart, "")
		r.printRemoved(removed)
	default:
		r.rep.existed("greeter autostart 无 swayidle 行", greeterAutostart)
	}
	return nil
}

func (r *run) checkSystemAutostart() error {
	b, err := r.FS.ReadFile(systemAutostart)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 %s: %w", systemAutostart, err)
	}
	if labwc.HasSwayidle(string(b)) {
		r.rep.warn("系统 autostart 含 swayidle 行，install 不修改该文件，请手工处理", systemAutostart)
	}
	return nil
}

func (r *run) installCursorTheme() error {
	themeDir := path.Join(r.kioskHome, ".icons", cursorThemeName)
	cursorDir := path.Join(themeDir, "cursors")
	if err := r.mkdirAll(cursorDir); err != nil {
		return err
	}
	files := map[string][]byte{path.Join(themeDir, "index.theme"): []byte(cursorThemeFile)}
	cur := xcursorBytes()
	for _, n := range cursorNames {
		files[path.Join(cursorDir, n)] = cur
	}
	written := 0
	for name, data := range files {
		if err := r.noSymlink(name); err != nil {
			return err
		}
		old, err := r.FS.ReadFile(name)
		if err == nil && string(old) == string(data) {
			continue
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("读取 %s: %w", name, err)
		}
		if err := r.FS.WriteFile(name, data, 0o644, &r.kioskOwner); err != nil {
			return fmt.Errorf("写入 %s: %w", name, err)
		}
		written++
	}
	if written == 0 {
		r.rep.existed("透明光标主题 "+themeDir, "")
	} else {
		r.rep.done("安装透明光标主题 "+themeDir, fmt.Sprintf("写入 %d 个文件", written))
	}
	return nil
}

func (r *run) setCursorEnv() error {
	file := path.Join(r.kioskHome, ".config/labwc/environment")
	changed, _, err := r.editText(file, true, func(old string) string {
		out, _ := labwc.SetEnv(old, "XCURSOR_THEME", cursorThemeName)
		return out
	})
	if err != nil {
		return err
	}
	if changed {
		r.rep.done("设置 XCURSOR_THEME="+cursorThemeName, file)
	} else {
		r.rep.existed("XCURSOR_THEME="+cursorThemeName, file)
	}
	return nil
}

func (r *run) printRemoved(lines []string) {
	for _, l := range lines {
		_, _ = fmt.Fprintf(r.Out, "    已删除原行：%s\n", l)
	}
}

// mkdirAll 在桌面用户的家目录下逐级创建缺失的目录（0755，属主为桌面用户）。
func (r *run) mkdirAll(dir string) error {
	rel, err := relUnder(r.kioskHome, dir)
	if err != nil {
		return err
	}
	cur := r.kioskHome
	for _, seg := range strings.Split(rel, "/") {
		cur = path.Join(cur, seg)
		if err := r.noSymlink(cur); err != nil {
			return err
		}
		fi, err := r.FS.Stat(cur)
		switch {
		case err == nil && fi.IsDir:
			continue
		case err == nil:
			return fmt.Errorf("%s 已存在但不是目录", cur)
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("检查 %s: %w", cur, err)
		}
		if err := r.FS.Mkdir(cur, 0o755, r.kioskOwner); err != nil {
			return fmt.Errorf("创建 %s: %w", cur, err)
		}
	}
	return nil
}

func relUnder(base, p string) (string, error) {
	rel, ok := strings.CutPrefix(p, base+"/")
	if !ok || rel == "" {
		return "", fmt.Errorf("%s 不在 %s 之下", p, base)
	}
	return rel, nil
}

// editText 读取文本文件，交给 edit 生成新内容；有变化才写。已存在的文件改前先备份、写回时保留原属主与权限；
// create 为真时文件不存在则新建（0644、桌面用户属主，缺失的父目录一并创建），否则视为不存在直接返回。
func (r *run) editText(file string, create bool, edit func(old string) string) (changed, existed bool, err error) {
	if create {
		if err := r.mkdirAll(path.Dir(file)); err != nil {
			return false, false, err
		}
		if err := r.noSymlink(file); err != nil {
			return false, false, err
		}
	}
	fi, err := r.FS.Stat(file)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return false, false, nil
		}
		if err := r.FS.WriteFile(file, []byte(edit("")), 0o644, &r.kioskOwner); err != nil {
			return false, false, fmt.Errorf("写入 %s: %w", file, err)
		}
		return true, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("检查 %s: %w", file, err)
	}
	old, err := r.FS.ReadFile(file)
	if err != nil {
		return false, true, fmt.Errorf("读取 %s: %w", file, err)
	}
	next := edit(string(old))
	if next == string(old) {
		return false, true, nil
	}
	bak, err := r.backup(file, fi, old)
	if err != nil {
		return false, true, err
	}
	if bak != "" {
		_, _ = fmt.Fprintf(r.Out, "    已备份 %s\n", bak)
	}
	if err := r.FS.WriteFile(file, []byte(next), fi.Mode, &Owner{UID: fi.UID, GID: fi.GID}); err != nil {
		return false, true, fmt.Errorf("写入 %s: %w", file, err)
	}
	return true, true, nil
}

// backup 在原文件旁写 <文件>.pimon-bak-<UTC 时间>；同目录里已有同内容的备份时不重复建，返回空串。
func (r *run) backup(file string, fi FileInfo, content []byte) (string, error) {
	dir, base := path.Dir(file), path.Base(file)
	names, err := r.FS.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("读取目录 %s: %w", dir, err)
	}
	for _, n := range names {
		if !strings.HasPrefix(n, base+".pimon-bak-") {
			continue
		}
		cand := path.Join(dir, n)
		if li, err := r.FS.Lstat(cand); err != nil || li.IsSymlink {
			continue
		}
		if b, err := r.FS.ReadFile(cand); err == nil && string(b) == string(content) {
			return "", nil
		}
	}
	bak := file + ".pimon-bak-" + r.Clock.Now().UTC().Format(backupTimeLayout)
	if err := r.FS.WriteFile(bak, content, fi.Mode, &Owner{UID: fi.UID, GID: fi.GID}); err != nil {
		return "", fmt.Errorf("写备份 %s: %w", bak, err)
	}
	return bak, nil
}
