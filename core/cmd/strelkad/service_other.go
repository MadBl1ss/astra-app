//go:build !windows

package main

import (
	"fmt"
	"os"
)

// On Linux the daemon runs under systemd as a plain foreground process; the
// unit file and its install arrive in stage 09.
func underServiceManager() bool { return false }

func runService(cfg runConfig) int { return runConsole(cfg) }

func serviceCmd([]string) int {
	fmt.Fprintln(os.Stderr, "На Linux службой управляет systemd: systemctl start|stop strelka.")
	return 2
}
