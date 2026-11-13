//go:build linux

package main

import (
	"os/exec"
	"syscall"
)

func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interrupt(pid int) error { return syscall.Kill(pid, syscall.SIGINT) }
