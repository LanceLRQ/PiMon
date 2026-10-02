//go:build linux

package kiosk

import (
	"syscall"
	"testing"
)

func TestLaunchAttr_Linux上父进程退出时发SIGTERM(t *testing.T) {
	if a := launchAttr(); a.Pdeathsig != syscall.SIGTERM {
		t.Fatalf("Pdeathsig=%v", a.Pdeathsig)
	}
}
