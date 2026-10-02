package install

// unitContent 是 /etc/systemd/system/pimon-hub.service 的完整内容，字段取值见 M1e Ruling 20：
// Type=notify 配合 WatchdogSec 由 hub 自己喂狗；不设 PrivateDevices/DevicePolicy/RestrictAddressFamilies，
// 因为读取树莓派状态需要 /dev/vchiq，网络检测需要 netlink 等地址族。
const unitContent = `[Unit]
Description=PiMon hub
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
NotifyAccess=main
ExecStart=/usr/local/bin/pimon-hub serve
User=pimon
Group=pimon
SupplementaryGroups=video
Restart=always
RestartSec=2s
WatchdogSec=30s
TimeoutStopSec=20s
ProtectSystem=strict
ReadWritePaths=/var/lib/pimon
ProtectHome=yes
PrivateTmp=yes
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
`
