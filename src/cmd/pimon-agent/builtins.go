package main

// agent 带的内置插件靠各自包的 init 注册，这里集中空导入。只导入能在 agent 上运行的插件；
// core、demo、hubself、netreach、weather 只在 hub 运行（hubself 还带全局绑定），不导入。
import (
	_ "github.com/LanceLRQ/PiMon/src/plugins/docker"
	_ "github.com/LanceLRQ/PiMon/src/plugins/hostmetrics"
	_ "github.com/LanceLRQ/PiMon/src/plugins/httpcheck"
	_ "github.com/LanceLRQ/PiMon/src/plugins/httpjson"
	_ "github.com/LanceLRQ/PiMon/src/plugins/ping"
	_ "github.com/LanceLRQ/PiMon/src/plugins/storagehealth"
	_ "github.com/LanceLRQ/PiMon/src/plugins/systemd"
	_ "github.com/LanceLRQ/PiMon/src/plugins/tcpcheck"
)
