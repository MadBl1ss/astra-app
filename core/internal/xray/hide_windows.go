//go:build windows

package xray

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps a console window from flashing up when a daemon without
// a console (a service) runs the check.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
