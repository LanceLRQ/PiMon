package sudocmd

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// sudoersUser 限定写入 sudoers 的用户名：小写开头的普通用户名，排除 ALL、%组、#uid 等特殊写法。
var sudoersUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// SudoersFile 渲染 /etc/sudoers.d 下的完整文件内容：文件头注释、关闭 PAM 会话记录的 Defaults
// （保留 COMMAND 审计日志，不写 !syslog），以及每条 Specs 规格一行的 NOPASSWD 规则，运行身份只限 root。
// paths 为空时报错（没有可放行的命令就不应写文件）。
func SudoersFile(user string, paths map[string]string) (string, error) {
	if !sudoersUser.MatchString(user) {
		return "", fmt.Errorf("sudoers 用户名 %q 不合法", user)
	}
	if len(paths) == 0 {
		return "", errors.New("没有可放行的命令")
	}
	specs, err := Specs(paths)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# 由 pimon-agent install 生成，请勿手工修改；pimon-agent uninstall 会删除本文件。\n")
	b.WriteString("# 只放行 storage-health 插件以 sudo -n 执行的只读命令，参数逐字精确匹配，不含通配符。\n")
	b.WriteString("# 保留 sudo 的 COMMAND 审计日志，只关闭每次执行产生的 PAM 会话记录。\n")
	b.WriteString("Defaults:" + user + " !pam_session\n")
	for _, s := range specs {
		b.WriteString(user + " ALL=(root) NOPASSWD: " + s + "\n")
	}
	return b.String(), nil
}
