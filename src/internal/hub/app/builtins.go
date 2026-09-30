package app

// 内置插件靠各自包的 init 注册，这里集中空导入，保证任何二进制引用 app 时都带上全部内置插件。
import (
	_ "github.com/LanceLRQ/PiMon/src/plugins/core"
	_ "github.com/LanceLRQ/PiMon/src/plugins/demo"
	_ "github.com/LanceLRQ/PiMon/src/plugins/httpjson"
	_ "github.com/LanceLRQ/PiMon/src/plugins/hubself"
)
