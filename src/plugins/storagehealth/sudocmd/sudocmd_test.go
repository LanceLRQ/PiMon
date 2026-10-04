package sudocmd

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// globToRegexp 是测试侧独立实现的最小 sudoers 通配匹配器：只支持字面字符与 [...]，
// 遇到 *、?、\ 等其它通配或转义直接判失败，借此证明生成的模式里没有它们。
func globToRegexp(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*', '?', '\\':
			t.Fatalf("模式 %q 含不允许的通配或转义字符 %q", pattern, c)
		case '[':
			j := strings.IndexByte(pattern[i:], ']')
			if j < 0 {
				t.Fatalf("模式 %q 的 [ 未闭合", pattern)
			}
			body := pattern[i+1 : i+j]
			if body == "" || strings.ContainsAny(body, "!^[\\") {
				t.Fatalf("模式 %q 的字符类 %q 不是简单字符类", pattern, body)
			}
			b.WriteString("[" + body + "]")
			i += j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// specMatches 模拟 sudo 的匹配：命令路径逐字相等，参数按空格拼成一个串后整串匹配模式。
func specMatches(t *testing.T, spec, path string, args []string) bool {
	t.Helper()
	cmd, argPattern, _ := strings.Cut(spec, " ")
	if cmd != path {
		return false
	}
	return globToRegexp(t, argPattern).MatchString(strings.Join(args, " "))
}

var testPaths = map[string]string{
	Smartctl: "/usr/sbin/smartctl",
	Mdadm:    "/usr/sbin/mdadm",
	Btrfs:    "/usr/bin/btrfs",
}

func TestSpecs不含星号且只含安全字符(t *testing.T) {
	specs, err := Specs(testPaths)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) == 0 {
		t.Fatal("应生成命令规格")
	}
	safe := regexp.MustCompile(`^[A-Za-z0-9/._+\-\[\] ]+$`)
	for _, s := range specs {
		if strings.ContainsAny(s, "*?\\,:=!\"'\t\n") {
			t.Errorf("规格 %q 含 sudoers 特殊字符", s)
		}
		if !safe.MatchString(s) {
			t.Errorf("规格 %q 含意外字符", s)
		}
		_, argPattern, _ := strings.Cut(s, " ")
		globToRegexp(t, argPattern)
	}
}

func TestSpecs只为已解析的命令生成(t *testing.T) {
	specs, err := Specs(map[string]string{Smartctl: "/usr/sbin/smartctl"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range specs {
		if !strings.HasPrefix(s, "/usr/sbin/smartctl ") {
			t.Errorf("不应生成 %q", s)
		}
	}
	if !contains(specs, "/usr/sbin/smartctl --scan-open") {
		t.Errorf("缺少 --scan-open: %v", specs)
	}
}

func TestSpecs拒绝不安全的命令路径(t *testing.T) {
	for _, p := range []string{"smartctl", "/usr/sbin/smart ctl", "/usr/sbin/a,b", "/usr/../tmp/smartctl", "/usr/sbin/x:y", "/usr/sbin/a*"} {
		if _, err := Specs(map[string]string{Smartctl: p}); err == nil {
			t.Errorf("路径 %q 应被拒绝", p)
		}
	}
	if _, err := Specs(map[string]string{"rm": "/bin/rm"}); err == nil {
		t.Error("未知命令应被拒绝")
	}
}

func TestSpecs全表(t *testing.T) {
	specs, err := Specs(testPaths)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/usr/sbin/smartctl --scan-open",
		"/usr/sbin/smartctl -j -a -n standby /dev/sd[a-z]",
		"/usr/sbin/smartctl -j -a -n standby /dev/nvme[0-9]",
		"/usr/sbin/smartctl -j -a -n standby -d sat /dev/sd[a-z][a-z]",
		"/usr/sbin/mdadm --detail /dev/md[0-9][0-9][0-9]",
		"/usr/bin/btrfs device stats /dev/dm-[0-9]",
		"/usr/bin/btrfs scrub status /dev/nvme[0-9]n[0-9]p[0-9]",
		"/usr/bin/btrfs device stats /dev/sd[a-z][0-9]",
	}
	for _, w := range want {
		if !contains(specs, w) {
			t.Errorf("缺少规格 %q", w)
		}
	}
	seen := map[string]bool{}
	for _, s := range specs {
		if seen[s] {
			t.Errorf("规格重复 %q", s)
		}
		seen[s] = true
	}
}

// 每个合法 argv 都能被某条生成的规格匹配，且插件侧生成的 argv 与规格逐字一致。
func TestArgs合法设备均被规格匹配(t *testing.T) {
	specs, err := Specs(testPaths)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		op  Op
		dev string
	}{
		{OpSmartScan, ""},
		{OpSmart, "/dev/sda"}, {OpSmart, "/dev/sdz"}, {OpSmart, "/dev/sdab"},
		{OpSmart, "/dev/nvme0"}, {OpSmart, "/dev/nvme12"}, {OpSmart, "/dev/nvme0n1"}, {OpSmart, "/dev/nvme10n1"},
		{OpSmartSat, "/dev/sdb"}, {OpSmartSat, "/dev/sdaa"},
		{OpMdadmDetail, "/dev/md0"}, {OpMdadmDetail, "/dev/md127"}, {OpMdadmDetail, "/dev/md99"},
		{OpBtrfsDeviceStats, "/dev/dm-0"}, {OpBtrfsDeviceStats, "/dev/dm-123"}, {OpBtrfsDeviceStats, "/dev/md127"},
		{OpBtrfsDeviceStats, "/dev/sda1"}, {OpBtrfsDeviceStats, "/dev/sdb12"}, {OpBtrfsDeviceStats, "/dev/sdc"},
		{OpBtrfsDeviceStats, "/dev/nvme0n1p2"}, {OpBtrfsDeviceStats, "/dev/nvme0n1"}, {OpBtrfsDeviceStats, "/dev/mmcblk0p2"},
		{OpBtrfsScrubStatus, "/dev/dm-3"}, {OpBtrfsScrubStatus, "/dev/sdaa1"}, {OpBtrfsScrubStatus, "/dev/nvme10n1p12"},
	}
	for _, c := range cases {
		args, err := Args(c.op, c.dev)
		if err != nil {
			t.Errorf("%s %q 应合法: %v", c.op, c.dev, err)
			continue
		}
		path := testPaths[commandOf(t, c.op)]
		matched := 0
		for _, s := range specs {
			if specMatches(t, s, path, args) {
				matched++
			}
		}
		if matched != 1 {
			t.Errorf("%s %v 被 %d 条规格匹配，期望恰好 1 条", c.op, args, matched)
		}
	}
}

func TestArgs精确形态(t *testing.T) {
	cases := []struct {
		op   Op
		dev  string
		want string
	}{
		{OpSmartScan, "", "--scan-open"},
		{OpSmart, "/dev/sda", "-j -a -n standby /dev/sda"},
		{OpSmartSat, "/dev/sdb", "-j -a -n standby -d sat /dev/sdb"},
		{OpMdadmDetail, "/dev/md0", "--detail /dev/md0"},
		{OpBtrfsDeviceStats, "/dev/dm-0", "device stats /dev/dm-0"},
		{OpBtrfsScrubStatus, "/dev/dm-0", "scrub status /dev/dm-0"},
	}
	for _, c := range cases {
		args, err := Args(c.op, c.dev)
		if err != nil || strings.Join(args, " ") != c.want {
			t.Errorf("%s %q = %v %v，期望 %q", c.op, c.dev, args, err, c.want)
		}
	}
}

// 注入尝试在插件侧校验处就被拒绝；即使绕过校验拼成 argv，也没有任何规格能匹配。
func TestArgs拒绝注入(t *testing.T) {
	specs, err := Specs(testPaths)
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{
		"", "/dev/sda -s off", "/dev/sda --set=standby,now", "-s off", "/dev/sda -t long",
		"/dev/sda;reboot", "/dev/sda ", " /dev/sda", "/dev/../etc/shadow", "../dev/sda", "/dev/sda/../sdb",
		"/dev/sdA", "/dev/sd1", "/dev/sdabc", "/dev/nvme0n1p1extra", "/dev/md0 --zero-superblock",
		"--zero-superblock", "/dev/dm-0 -z", "-z", "/dev/sda\n", "/dev/sda\t", "/etc/passwd", "/dev/disk/by-id/ata-x",
		"/dev/md", "/dev/md1234", "/dev/dm-", "/dev/dm-1234", "/dev/nvme", "/dev/nvme123",
	}
	ops := []Op{OpSmart, OpSmartSat, OpMdadmDetail, OpBtrfsDeviceStats, OpBtrfsScrubStatus}
	for _, op := range ops {
		path := testPaths[commandOf(t, op)]
		for _, d := range bad {
			if _, err := Args(op, d); err == nil {
				t.Errorf("%s 应拒绝 %q", op, d)
			}
			// 绕过校验直接拼接（sudo 按空格拼接参数后匹配）：规格也不应放行。
			raw := append(templateOf(t, op), d)
			for _, s := range specs {
				if specMatches(t, s, path, raw) {
					t.Errorf("规格 %q 放行了注入 argv %v", s, raw)
				}
			}
		}
	}
	// 写操作形态：没有任何规格能匹配。
	writes := [][]string{
		{"-s", "off", "/dev/sda"}, {"-t", "long", "/dev/sda"}, {"--set=standby,now", "/dev/sda"},
		{"-j", "-a", "-n", "standby", "-s", "off", "/dev/sda"}, {"-j", "-a", "-n", "standby", "/dev/sda", "-s", "off"},
	}
	for _, w := range writes {
		for _, s := range specs {
			if specMatches(t, s, testPaths[Smartctl], w) {
				t.Errorf("规格 %q 放行了写操作 %v", s, w)
			}
		}
	}
	for _, w := range [][]string{{"--zero-superblock", "/dev/md0"}, {"--detail", "/dev/md0", "--zero-superblock"}, {"--stop", "/dev/md0"}} {
		for _, s := range specs {
			if specMatches(t, s, testPaths[Mdadm], w) {
				t.Errorf("规格 %q 放行了 %v", s, w)
			}
		}
	}
	for _, w := range [][]string{{"device", "stats", "-z", "/dev/dm-0"}, {"device", "stats", "/dev/dm-0", "-z"}, {"scrub", "start", "/dev/dm-0"}, {"scrub", "cancel", "/dev/dm-0"}} {
		for _, s := range specs {
			if specMatches(t, s, testPaths[Btrfs], w) {
				t.Errorf("规格 %q 放行了 %v", s, w)
			}
		}
	}
}

func TestArgs扫描不接受设备(t *testing.T) {
	if _, err := Args(OpSmartScan, "/dev/sda"); err == nil {
		t.Fatal("--scan-open 不应带设备")
	}
	if _, err := Args(Op("nope"), "/dev/sda"); err == nil {
		t.Fatal("未知形态应被拒绝")
	}
}

func TestArgs返回副本(t *testing.T) {
	a, _ := Args(OpSmart, "/dev/sda")
	a[0] = "-s"
	b, _ := Args(OpSmart, "/dev/sda")
	if b[0] != "-j" {
		t.Fatal("Args 必须返回副本，不得共享模板")
	}
}

func TestSudoArgv(t *testing.T) {
	got := SudoArgv("/usr/sbin/smartctl", []string{"--scan-open"})
	if strings.Join(got, " ") != "-n -- /usr/sbin/smartctl --scan-open" {
		t.Fatalf("SudoArgv = %v", got)
	}
}

func TestCanonicalize(t *testing.T) {
	links := map[string]string{
		"/dev/disk/by-id/ata-WDC_X": "/dev/sda",
		"/dev/mapper/trim_abc-0":    "/dev/dm-2",
		"/dev/md/fnOS:0":            "/dev/md127",
		"/dev/disk/by-id/evil":      "/etc/shadow",
		"/dev/disk/by-id/sub":       "/dev/block/8:0",
	}
	eval := func(p string) (string, error) {
		if r, ok := links[p]; ok {
			return r, nil
		}
		if strings.HasPrefix(p, "/dev/disk/") {
			return "", errors.New("no such file")
		}
		return p, nil
	}
	good := map[string]string{
		"/dev/sda":                  "/dev/sda",
		" /dev/sdb ":                "/dev/sdb",
		"/dev/disk/by-id/ata-WDC_X": "/dev/sda",
		"/dev/mapper/trim_abc-0":    "/dev/dm-2",
		"/dev/md/fnOS:0":            "/dev/md127",
	}
	for in, want := range good {
		got, err := Canonicalize(in, eval)
		if err != nil || got != want {
			t.Errorf("Canonicalize(%q) = %q %v，期望 %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "sda", "/etc/passwd", "/dev/../etc/shadow", "/dev/disk/by-id/evil", "/dev/disk/by-id/missing", "/dev/disk/by-id/sub", "/dev/sda -s off"} {
		if got, err := Canonicalize(in, eval); err == nil {
			t.Errorf("Canonicalize(%q) 应失败，得到 %q", in, got)
		}
	}
}

func TestMatchDevice(t *testing.T) {
	if !MatchDevice(OpSmart, "/dev/nvme0n1") || MatchDevice(OpSmartSat, "/dev/nvme0n1") {
		t.Fatal("-d sat 只适用于 sd 设备")
	}
	if MatchDevice(OpSmartScan, "") {
		t.Fatal("扫描没有设备模式")
	}
}

func TestDevicePatterns每个字符类只匹配一个字符(t *testing.T) {
	for _, op := range []Op{OpSmart, OpSmartSat, OpMdadmDetail, OpBtrfsDeviceStats, OpBtrfsScrubStatus} {
		for _, p := range DevicePatterns(op) {
			re := globToRegexp(t, p)
			if re.MatchString(strings.Replace(p, "[", "", 1)) {
				t.Errorf("模式 %q 的字符类不应匹配字面串", p)
			}
			if !strings.HasPrefix(p, "/dev/") {
				t.Errorf("模式 %q 不在 /dev 下", p)
			}
		}
	}
}

func TestMatchGlob与测试匹配器一致(t *testing.T) {
	samples := []string{"/dev/sda", "/dev/sdab", "/dev/sd1", "/dev/nvme0n1", "/dev/nvme0n1p1", "/dev/md127", "/dev/dm-12", "/dev/sda1", "/dev/mmcblk0p2", "/dev/sd", "/dev/sda ", "/dev/sd-"}
	for _, op := range []Op{OpSmart, OpSmartSat, OpMdadmDetail, OpBtrfsDeviceStats, OpBtrfsScrubStatus} {
		for _, p := range DevicePatterns(op) {
			re := globToRegexp(t, p)
			for _, s := range samples {
				if matchGlob(p, s) != re.MatchString(s) {
					t.Errorf("matchGlob(%q, %q) 与测试匹配器不一致", p, s)
				}
			}
		}
	}
}

func commandOf(t *testing.T, op Op) string {
	t.Helper()
	s, ok := lookup(op)
	if !ok {
		t.Fatalf("未知形态 %s", op)
	}
	return s.command
}

func templateOf(t *testing.T, op Op) []string {
	t.Helper()
	s, _ := lookup(op)
	return append([]string(nil), s.args...)
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
