package install

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

// systemUnit 是 /etc/systemd/system/pimon-agent.service 的完整内容。
// 与 hub 的单元不同，这里不能开 NoNewPrivileges（见单元内注释）；RuntimeDirectory 让 ProtectSystem=strict
// 下的 /run/pimon-agent（控制套接字所在）可写，属主为 pimon。
const systemUnit = `[Unit]
Description=PiMon agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/pimon-agent run
User=pimon
Group=pimon
Restart=always
RestartSec=2s
RuntimeDirectory=pimon-agent
RuntimeDirectoryMode=0750
ProtectSystem=strict
# 守护进程会原子改写 /etc/pimon-agent/agent.json（同目录临时文件 + rename），配置目录也要可写。
ReadWritePaths=/var/lib/pimon-agent /etc/pimon-agent
PrivateTmp=yes
# 不设置 NoNewPrivileges=yes：storage-health 插件（M2d）要经 sudo 调用 smartctl 等命令，
# 开启后 sudo 无法提权。

[Install]
WantedBy=multi-user.target
`

// userUnit 生成 Linux 用户模式的 systemd 用户单元；run 必须带 --user，守护进程才会按用户模式解析路径。
func userUnit(bin string) (string, error) {
	exe, err := systemdArg(bin)
	if err != nil {
		return "", err
	}
	return `[Unit]
Description=PiMon agent（用户模式）

[Service]
ExecStart=` + exe + ` run --user
Restart=always
RestartSec=2s

[Install]
WantedBy=default.target
`, nil
}

// systemdArg 把路径写成 ExecStart 里的单个参数：% 与 $ 按 systemd 规则转义为 %% 与 $$，
// 含空白、引号或反斜杠时整体加双引号；含控制字符时拒绝（无法安全表达）。
func systemdArg(s string) (string, error) {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("路径 %q 含控制字符，无法写进 systemd 单元", s)
		}
	}
	esc := strings.NewReplacer("%", "%%", "$", "$$").Replace(s)
	if !strings.ContainsAny(s, " \t\"'\\") {
		return esc, nil
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(esc) + `"`, nil
}

// launchdPlist 生成 LaunchAgent 的 plist：RunAtLoad + KeepAlive，日志交给守护进程的 --log-file 自行轮转。
func launchdPlist(bin, logFile string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- PiMon agent。日志由守护进程写入 --log-file 指定的文件：超过 5 MiB 时改名为 .1 再重开，只保留一个旧文件。
     不使用 StandardOutPath/StandardErrorPath，因为 launchd 不会轮转它们。 -->
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + xmlText(LaunchdLabel) + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlText(bin) + `</string>
		<string>run</string>
		<string>--log-file</string>
		<string>` + xmlText(logFile) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`
}

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
