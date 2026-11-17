//go:build !windows

package store

import "os"

// Secure creates dir private to its owner. The service's ownership and mode
// on Linux (root, 0700) arrive with systemd in stage 09.
func Secure(dir string) (prior string, err error) {
	return "", os.MkdirAll(dir, 0o700)
}

// ShareReports creates the reports folder; who may read it on Linux is
// decided in stage 09.
func ShareReports(dir string) error {
	return os.MkdirAll(dir, 0o755)
}
