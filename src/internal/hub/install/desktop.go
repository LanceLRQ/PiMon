package install

import (
	"context"
	"errors"
	"fmt"

	"github.com/LanceLRQ/PiMon/src/internal/lightdm"
)

func detectAutologinUser(fsys FS) (string, error) { return lightdm.AutologinUser(fsys) }

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
