//go:build !windows

package runx

import "os/exec"

func hideWindow(*exec.Cmd) {}
