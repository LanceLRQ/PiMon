// pimon-hub 是 PiMon 的中枢服务入口。
package main

import (
	"fmt"
	"os"

	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println(version.Version)
		return
	}
	fmt.Fprintln(os.Stderr, "用法: pimon-hub version")
	os.Exit(2)
}
