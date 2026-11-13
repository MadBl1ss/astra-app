//go:build !windows

package ipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Address turns a short channel name into a socket path. A name with a slash
// is already a path (the installed service passes /run/strelka/strelkad.sock).
func Address(name string) string {
	if strings.ContainsRune(name, '/') {
		return name
	}
	return filepath.Join(os.TempDir(), name+".sock")
}

// Listen opens the channel as a unix socket.
func Listen(name string) (net.Listener, error) {
	path := Address(name)
	// A socket file outlives a daemon that was killed. If nobody answers on
	// it, it is a leftover; if somebody does, another daemon is running.
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		return nil, &net.OpError{Op: "listen", Net: "unix", Err: os.ErrExist}
	}
	os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// Owner and group only; who is in the group is the installer's business.
	if err := os.Chmod(path, 0o660); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func dial(ctx context.Context, name string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", Address(name))
}
