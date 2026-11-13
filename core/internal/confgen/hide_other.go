//go:build !windows

package confgen

import "os/exec"

func hideWindow(*exec.Cmd) {}
