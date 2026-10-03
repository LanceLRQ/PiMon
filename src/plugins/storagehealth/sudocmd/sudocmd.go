// Package sudocmd 是 storage-health 经 sudo 执行的只读命令的唯一事实来源：
// 命令候选路径与属主校验、插件调用用的 argv、sudoers 命令规格、设备名规范化与白名单。
//
// argv 与 sudoers 规格由同一张形态表生成：固定参数逐字相同，设备位置在 argv 里是具体设备名、
// 在规格里是设备模式。设备模式只用字面字符与单字符字符类 [...]（不用 *：sudoers 参数里的 *
// 能匹配空白与 /，会放行额外参数），插件侧先用同一模式校验设备名，校验不过就不执行 sudo。
//
// 本包没有 init 副作用，只依赖标准库；agent 的 install 用 Specs 渲染 sudoers。
package sudocmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// 需要 root 的命令名。
const (
	Smartctl = "smartctl"
	Mdadm    = "mdadm"
	Btrfs    = "btrfs"
)

// Commands 按固定顺序列出全部命令名。
var Commands = []string{Smartctl, Mdadm, Btrfs}

// Op 是一种放行的只读命令形态。
type Op string

// 全部放行形态。
const (
	OpSmartScan        Op = "smartctl-scan"      // smartctl --scan-open
	OpSmart            Op = "smartctl"           // smartctl -j -a -n standby <设备>
	OpSmartSat         Op = "smartctl-sat"       // smartctl -j -a -n standby -d sat <设备>
	OpMdadmDetail      Op = "mdadm-detail"       // mdadm --detail <设备>
	OpBtrfsDeviceStats Op = "btrfs-device-stats" // btrfs device stats <设备>
	OpBtrfsScrubStatus Op = "btrfs-scrub-status" // btrfs scrub status <设备>
)

// 设备模式。每个 [...] 只匹配一个字符，需要几位就枚举几档。
var (
	sdDisks  = []string{"/dev/sd[a-z]", "/dev/sd[a-z][a-z]"}
	nvmeCtrl = []string{"/dev/nvme[0-9]", "/dev/nvme[0-9][0-9]"}
	nvmeNS   = []string{"/dev/nvme[0-9]n[0-9]", "/dev/nvme[0-9][0-9]n[0-9]"}
	mdArrays = []string{"/dev/md[0-9]", "/dev/md[0-9][0-9]", "/dev/md[0-9][0-9][0-9]"}
	dmDevs   = []string{"/dev/dm-[0-9]", "/dev/dm-[0-9][0-9]", "/dev/dm-[0-9][0-9][0-9]"}
	sdParts  = []string{
		"/dev/sd[a-z][0-9]", "/dev/sd[a-z][0-9][0-9]",
		"/dev/sd[a-z][a-z][0-9]", "/dev/sd[a-z][a-z][0-9][0-9]",
	}
	nvmeParts = []string{
		"/dev/nvme[0-9]n[0-9]p[0-9]", "/dev/nvme[0-9]n[0-9]p[0-9][0-9]",
		"/dev/nvme[0-9][0-9]n[0-9]p[0-9]", "/dev/nvme[0-9][0-9]n[0-9]p[0-9][0-9]",
	}
	mmcParts = []string{"/dev/mmcblk[0-9]p[0-9]", "/dev/mmcblk[0-9]p[0-9][0-9]"}
)

func join(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// btrfsDevs 是 btrfs 挂载点解析出的规范设备：LVM（dm-N）、md 阵列、整盘或分区。
var btrfsDevs = join(dmDevs, mdArrays, sdDisks, sdParts, nvmeNS, nvmeParts, mmcParts)

type opSpec struct {
	op      Op
	command string
	args    []string // 固定参数；有设备时设备追加在最后
	devices []string // 设备模式；nil 表示不带设备
}

// opTable 的顺序即 Specs 的输出顺序。
var opTable = []opSpec{
	{OpSmartScan, Smartctl, []string{"--scan-open"}, nil},
	{OpSmart, Smartctl, []string{"-j", "-a", "-n", "standby"}, join(sdDisks, nvmeCtrl, nvmeNS)},
	{OpSmartSat, Smartctl, []string{"-j", "-a", "-n", "standby", "-d", "sat"}, sdDisks},
	{OpMdadmDetail, Mdadm, []string{"--detail"}, mdArrays},
	{OpBtrfsDeviceStats, Btrfs, []string{"device", "stats"}, btrfsDevs},
	{OpBtrfsScrubStatus, Btrfs, []string{"scrub", "status"}, btrfsDevs},
}

func lookup(op Op) (opSpec, bool) {
	for _, s := range opTable {
		if s.op == op {
			return s, true
		}
	}
	return opSpec{}, false
}

// ErrDevice 表示设备名不在该形态的白名单内。
var ErrDevice = errors.New("设备不在白名单内")

// Args 返回形态 op 作用于 device 的参数（不含命令路径）。不带设备的形态要求 device 为空；
// 带设备的形态要求 device 逐字落在白名单模式内，否则返回 ErrDevice，调用方不得执行 sudo。
func Args(op Op, device string) ([]string, error) {
	s, ok := lookup(op)
	if !ok {
		return nil, fmt.Errorf("未知命令形态 %q", op)
	}
	args := append([]string(nil), s.args...)
	if s.devices == nil {
		if device != "" {
			return nil, fmt.Errorf("%s 不接受设备参数", op)
		}
		return args, nil
	}
	if !MatchDevice(op, device) {
		return nil, fmt.Errorf("%w：%q", ErrDevice, device)
	}
	return append(args, device), nil
}

// Command 返回形态 op 使用的命令名。
func Command(op Op) (string, bool) {
	s, ok := lookup(op)
	return s.command, ok
}

// DevicePatterns 返回形态 op 的设备模式副本。
func DevicePatterns(op Op) []string {
	s, _ := lookup(op)
	return append([]string(nil), s.devices...)
}

// MatchDevice 报告 device 是否逐字落在形态 op 的某个设备模式内。
func MatchDevice(op Op, device string) bool {
	s, ok := lookup(op)
	if !ok {
		return false
	}
	for _, p := range s.devices {
		if matchGlob(p, device) {
			return true
		}
	}
	return false
}

// SudoArgv 返回调用 sudo 的参数：`-n -- <命令绝对路径> <args…>`。-n 不交互，没权限立即失败。
func SudoArgv(path string, args []string) []string {
	return append([]string{"-n", "--", path}, args...)
}

// safePath 限定写入 sudoers 的命令路径字符，避免 sudoers 的特殊字符（, : = \ 空白 通配）。
var safePath = regexp.MustCompile(`^/[A-Za-z0-9._+\-/]+$`)

// ValidatePath 校验命令绝对路径可原样写入 sudoers。
func ValidatePath(p string) error {
	if !safePath.MatchString(p) || filepath.Clean(p) != p {
		return fmt.Errorf("命令路径 %q 不合法", p)
	}
	return nil
}

// Specs 为 paths（命令名 → 绝对路径，只含已解析到的命令）渲染 sudoers 命令规格，
// 每条形如 `<绝对路径> <固定参数…> [设备模式]`，与 Args/SudoArgv 生成的调用逐字对应。
// 顺序固定（按形态表与模式表），便于 install 比较内容是否变化。
func Specs(paths map[string]string) ([]string, error) {
	for name, p := range paths {
		if !isCommand(name) {
			return nil, fmt.Errorf("未知命令 %q", name)
		}
		if err := ValidatePath(p); err != nil {
			return nil, err
		}
	}
	var out []string
	for _, s := range opTable {
		p, ok := paths[s.command]
		if !ok {
			continue
		}
		prefix := p + " " + strings.Join(s.args, " ")
		if s.devices == nil {
			out = append(out, prefix)
			continue
		}
		for _, d := range s.devices {
			out = append(out, prefix+" "+d)
		}
	}
	return out, nil
}

func isCommand(name string) bool {
	for _, c := range Commands {
		if c == name {
			return true
		}
	}
	return false
}

// Canonicalize 把用户填写的设备路径（可为 /dev/disk/by-id/… 等符号链接）解析为 /dev 下的规范设备名。
// 解析结果必须直接位于 /dev 下（如 /dev/sda、/dev/dm-2），之后仍须经 MatchDevice/Args 校验。
func Canonicalize(p string, evalSymlinks func(string) (string, error)) (string, error) {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/dev/") || strings.ContainsAny(p, " \t\r\n") {
		return "", fmt.Errorf("设备路径 %q 必须是 /dev 下的绝对路径", p)
	}
	clean := filepath.Clean(p)
	if !strings.HasPrefix(clean, "/dev/") {
		return "", fmt.Errorf("设备路径 %q 不在 /dev 下", p)
	}
	real, err := evalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("无法解析设备路径 %q: %w", p, err)
	}
	real = filepath.Clean(real)
	if filepath.Dir(real) != "/dev" {
		return "", fmt.Errorf("设备路径 %q 解析为 %q，不是 /dev 下的规范设备名", p, real)
	}
	return real, nil
}

// matchGlob 判断 s 是否整串匹配模式 pattern。模式只含字面字符与单字符字符类 [...]
// （字符类内支持单字符与 a-z 区间），与 sudoers 对这两种语法的解释一致。
func matchGlob(pattern, s string) bool {
	i, j := 0, 0
	for i < len(pattern) {
		if j >= len(s) {
			return false
		}
		if pattern[i] != '[' {
			if pattern[i] != s[j] {
				return false
			}
			i++
			j++
			continue
		}
		end := strings.IndexByte(pattern[i:], ']')
		if end < 0 {
			return false
		}
		if !classHas(pattern[i+1:i+end], s[j]) {
			return false
		}
		i += end + 1
		j++
	}
	return j == len(s)
}

func classHas(body string, c byte) bool {
	for k := 0; k < len(body); k++ {
		if k+2 < len(body) && body[k+1] == '-' {
			if body[k] <= c && c <= body[k+2] {
				return true
			}
			k += 2
			continue
		}
		if body[k] == c {
			return true
		}
	}
	return false
}
