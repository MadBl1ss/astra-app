//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// ownGroup lets the test aim a console event at the daemon alone.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// interrupt is Ctrl+Break, which Go delivers as os.Interrupt just like
// Ctrl+C. Ctrl+C itself cannot be sent to a single process group.
func interrupt(pid int) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}
