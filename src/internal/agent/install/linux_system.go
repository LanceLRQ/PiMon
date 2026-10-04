package install

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
)

// dockerRisk 是把服务用户加入 docker 组时必须给出的提示。
const dockerRisk = "加入 docker 组近似授予 root 权限（docker 组成员可以挂载宿主机任意目录）"

// ensureUser 创建系统用户 pimon（已存在则跳过；hub 与 agent 可共用这个用户）。
func (r *run) ensureUser(ctx context.Context) error {
	u, err := r.Users.Lookup(serviceUser)
	switch {
	case err == nil:
		r.rep.Existed("系统用户 "+serviceUser, "")
	case errors.Is(err, sysinstall.ErrNotFound):
		if _, err := r.Runner.Run(ctx, cmdUseradd, "--system", "--user-group", "--home-dir", r.t.paths.DataDir,
			"--no-create-home", "--shell", "/usr/sbin/nologin", serviceUser); err != nil {
			return fmt.Errorf("创建系统用户 %s: %w", serviceUser, err)
		}
		if u, err = r.Users.Lookup(serviceUser); err != nil {
			return fmt.Errorf("创建后查询用户 %s: %w", serviceUser, err)
		}
		r.rep.Done("创建系统用户 "+serviceUser, "")
	default:
		return fmt.Errorf("查询用户 %s: %w", serviceUser, err)
	}
	r.pimon = u
	own := sysinstall.Owner(u)
	r.confOwn = &own
	return nil
}

// dockerGroup 在本机有 Docker 时决定是否把 pimon 加入 docker 组：显式 --docker-group 直接加；
// 交互终端询问；非交互且未给参数则跳过并警告。
func (r *run) dockerGroup(ctx context.Context) error {
	if _, err := r.FS.Stat(dockerSock); err != nil || !r.hasGroup("docker") {
		return nil // 本机没有 Docker
	}
	in, err := r.Users.InGroup(serviceUser, "docker")
	if err != nil {
		return fmt.Errorf("查询 %s 是否在 docker 组: %w", serviceUser, err)
	}
	if in {
		r.rep.Existed(serviceUser+" 已在 docker 组", "")
		return nil
	}
	if !r.o.DockerGroup {
		if !r.Interactive {
			r.rep.Warn("docker 组", "未把 "+serviceUser+" 加入 docker 组，Docker 相关插件将无法读取容器状态；非交互模式需显式加 --docker-group。注意："+dockerRisk)
			return nil
		}
		if !r.ask(fmt.Sprintf("本机有 Docker。是否把 %s 加入 docker 组，让 Docker 相关插件读取容器状态？\n注意：%s。[y/N] ", serviceUser, dockerRisk)) {
			r.rep.Skipped("docker 组", "未加入")
			return nil
		}
	}
	if _, err := r.Runner.Run(ctx, cmdUsermod, "-aG", "docker", serviceUser); err != nil {
		return fmt.Errorf("把 %s 加入 docker 组: %w", serviceUser, err)
	}
	r.groupChanged = true
	r.rep.Done(serviceUser+" 加入 docker 组", dockerRisk)
	return nil
}

// ask 输出问题并从共用读取器读一行，回答 y 或 yes（不分大小写）才返回真。
func (r *run) ask(question string) bool {
	_, _ = fmt.Fprint(r.Out, question)
	if r.in == nil {
		if r.In == nil {
			return false
		}
		r.in = bufio.NewReader(r.In)
	}
	ans, _ := r.in.ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(ans))
	return a == "y" || a == "yes"
}

// hasGroup 读 /etc/group 判断组是否存在。
func (r *run) hasGroup(name string) bool {
	b, err := r.FS.ReadFile(groupFile)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if g, _, ok := strings.Cut(line, ":"); ok && g == name {
			return true
		}
	}
	return false
}

// readPingRange 读取当前内核的 ping_group_range；返回 ok=false 表示读不到或范围为空（lo > hi）。
func readPingRange(fsys FS) (lo, hi int, ok bool, err error) {
	b, err := fsys.ReadFile(pingRangeProc)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("读取 %s: %w", pingRangeProc, err)
	}
	if n, _ := fmt.Sscan(string(b), &lo, &hi); n != 2 || lo > hi {
		return 0, 0, false, nil
	}
	return lo, hi, true, nil
}

// pingRangeAll 是写入 sysctl 文件的范围，与 hub install（/etc/sysctl.d/99-pimon.conf）一致：
// 两个文件开机按字母序加载，取值相同才不会后加载的把先加载的缩小；它也是最大范围，只扩大不缩小。
const pingRangeAll = "0 2147483647"

// ensureSysctl 让 ping 插件能用非特权 ICMP：当前范围不含 pimon 组时写 sysctl 文件并立即应用。
func (r *run) ensureSysctl(ctx context.Context) error {
	gid := r.pimon.GID
	lo, hi, ok, err := readPingRange(r.FS)
	if err != nil {
		return err
	}
	if ok && lo <= gid && gid <= hi {
		r.rep.Existed("net.ipv4.ping_group_range", "已包含 pimon 组")
		return nil
	}
	conf := "net.ipv4.ping_group_range = " + pingRangeAll + "\n"
	if err := r.FS.WriteFile(sysctlPath, []byte(conf), 0o644, nil); err != nil {
		return fmt.Errorf("写入 %s: %w", sysctlPath, err)
	}
	if _, err := r.Runner.Run(ctx, cmdSysctl, "-p", sysctlPath); err != nil {
		return fmt.Errorf("应用 %s: %w", sysctlPath, err)
	}
	r.rep.Done("放开 ping_group_range", pingRangeAll+"，写入 "+sysctlPath)
	return nil
}

// pingHint 是 Linux 用户模式的 ping_group_range 检查：不含当前用户的组时只提示，不改系统设置。
func (r *run) pingHint(_ context.Context) error {
	lo, hi, ok, err := readPingRange(r.FS)
	if err != nil {
		return err
	}
	if ok && lo <= r.EGID && r.EGID <= hi {
		r.rep.Existed("net.ipv4.ping_group_range", "已包含你的组")
		return nil
	}
	r.rep.Warn("net.ipv4.ping_group_range", fmt.Sprintf(
		"当前范围不含你的组（gid %d），ping 插件将不可用；如需使用请执行 sudo sysctl -w net.ipv4.ping_group_range=\"%s\"，并写入 /etc/sysctl.d/ 持久化",
		r.EGID, pingRangeAll))
	return nil
}
