//go:build linux

package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

type platformProcess struct{}

// lockThreadForChild pins the starting goroutine to its OS thread for the
// life of the child. Pdeathsig fires when the forking *thread* exits, not
// the process; without the lock the Go runtime may retire that thread and
// the kernel would kill a healthy engine.
func lockThreadForChild() { runtime.LockOSThread() }

// platformStart puts the engine in its own process group, so one signal
// reaches it and anything it started, and asks the kernel to kill it if the
// daemon dies.
func (p *process) platformStart() error {
	p.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGKILL,
	}
	return p.cmd.Start()
}

func (p *process) softStop() error { return syscall.Kill(-p.pid, syscall.SIGTERM) }

func (p *process) kill() { syscall.Kill(-p.pid, syscall.SIGKILL) }

func (p *process) release() {}

// FindByPath returns the PIDs of running processes whose executable is
// exactly exe. Never match by name: another copy of the same engine may be
// somebody's working VPN.
func FindByPath(exe string) ([]int, error) {
	want := normalizePath(exe)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		// Unreadable for processes of other users: not ours to judge.
		target, err := os.Readlink(filepath.Join("/proc", e.Name(), "exe"))
		if err == nil && target == want {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// KillByPath ends every process started from exe, except this one, and
// waits for each to go. It returns how many it ended.
func KillByPath(exe string) (int, error) {
	pids, err := FindByPath(exe)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, pid := range pids {
		if pid == os.Getpid() {
			continue
		}
		if syscall.Kill(pid, syscall.SIGKILL) != nil {
			continue
		}
		n++
		for deadline := time.Now().Add(3 * time.Second); Alive(pid) && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
	}
	return n, nil
}

func normalizePath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	return path
}

// Alive reports whether a process with this PID is still running.
func Alive(pid int) bool { return syscall.Kill(pid, 0) == nil }
