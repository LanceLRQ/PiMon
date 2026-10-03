package sudocmd

import (
	"strings"
	"testing"
)

func TestSudoersFile全文快照(t *testing.T) {
	got, err := SudoersFile("pimon", map[string]string{Mdadm: "/usr/sbin/mdadm"})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"# 由 pimon-agent install 生成，请勿手工修改；pimon-agent uninstall 会删除本文件。",
		"# 只放行 storage-health 插件以 sudo -n 执行的只读命令，参数逐字精确匹配，不含通配符。",
		"# 保留 sudo 的 COMMAND 审计日志，只关闭每次执行产生的 PAM 会话记录。",
		"Defaults:pimon !pam_session",
		"pimon ALL=(root) NOPASSWD: /usr/sbin/mdadm --detail /dev/md[0-9]",
		"pimon ALL=(root) NOPASSWD: /usr/sbin/mdadm --detail /dev/md[0-9][0-9]",
		"pimon ALL=(root) NOPASSWD: /usr/sbin/mdadm --detail /dev/md[0-9][0-9][0-9]",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("内容不符\n得到:\n%s\n期望:\n%s", got, want)
	}
}

func TestSudoersFile每条规则对应一条规格(t *testing.T) {
	paths := map[string]string{Smartctl: "/usr/sbin/smartctl", Mdadm: "/sbin/mdadm", Btrfs: "/usr/bin/btrfs"}
	specs, err := Specs(paths)
	if err != nil {
		t.Fatal(err)
	}
	got, err := SudoersFile("pimon", paths)
	if err != nil {
		t.Fatal(err)
	}
	var rules []string
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# "), line == "Defaults:pimon !pam_session":
		case strings.HasPrefix(line, "pimon ALL=(root) NOPASSWD: "):
			rules = append(rules, strings.TrimPrefix(line, "pimon ALL=(root) NOPASSWD: "))
		default:
			t.Fatalf("意外的行 %q", line)
		}
	}
	if strings.Join(rules, "\n") != strings.Join(specs, "\n") {
		t.Fatalf("规则与规格不一致\n%v\n%v", rules, specs)
	}
	if strings.Contains(got, "*") || strings.Contains(got, "!syslog") {
		t.Fatalf("不应含 * 或 !syslog:\n%s", got)
	}
}

func TestSudoersFile拒绝非法输入(t *testing.T) {
	for _, tc := range []struct {
		user  string
		paths map[string]string
	}{
		{"pimon", nil},
		{"pimon", map[string]string{}},
		{"", map[string]string{Mdadm: "/usr/sbin/mdadm"}},
		{"ALL", map[string]string{Mdadm: "/usr/sbin/mdadm"}},
		{"pi mon", map[string]string{Mdadm: "/usr/sbin/mdadm"}},
		{"%sudo", map[string]string{Mdadm: "/usr/sbin/mdadm"}},
		{"pimon", map[string]string{Mdadm: "/usr/sbin/md adm"}},
		{"pimon", map[string]string{"rm": "/bin/rm"}},
	} {
		if _, err := SudoersFile(tc.user, tc.paths); err == nil {
			t.Errorf("用户 %q 路径 %v 应报错", tc.user, tc.paths)
		}
	}
}
