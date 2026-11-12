package engine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"time"
)

// Launch is everything needed to start an engine once.
type Launch struct {
	Exe  string // full path; the supervisor never searches PATH
	Args []string
	Dir  string
	Env  []string // nil inherits the daemon's environment
}

// process is one running engine. The platform files add how it is confined
// (Job Object, process group) and how it is asked to stop.
type process struct {
	cmd     *exec.Cmd
	pid     int
	started time.Time

	exited  chan struct{} // closed once Wait has returned
	waitErr error         // valid after exited is closed
	drained chan struct{} // closed once all of its output has been read

	plat platformProcess
}

// startProcess starts l with stdout and stderr going to out. The exit is
// reported by closing p.exited: a goroutine blocks in Wait, so the
// supervisor learns of a crash at once instead of by polling.
func startProcess(l Launch, out *os.File) (*process, error) {
	cmd := exec.Command(l.Exe, l.Args...)
	// exec.Command resolves a bare name through PATH; engines are only ever
	// started from the engines folder, by full path.
	cmd.Path = l.Exe
	cmd.Err = nil
	cmd.Dir = l.Dir
	cmd.Env = l.Env
	cmd.Stdout = out
	cmd.Stderr = out

	p := &process{cmd: cmd, exited: make(chan struct{})}
	started := make(chan error, 1)
	go func() {
		// On Linux the parent-death signal is tied to the thread that
		// forked the child, not to the process; see platformStart.
		lockThreadForChild()
		if err := p.platformStart(); err != nil {
			started <- err
			return
		}
		p.pid = cmd.Process.Pid
		p.started = time.Now()
		started <- nil
		p.waitErr = cmd.Wait()
		close(p.exited)
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return p, nil
}

// exitCode is the code the process ended with, or -1 if it was killed by a
// signal or never reported one.
func (p *process) exitCode() int {
	var ee *exec.ExitError
	if errors.As(p.waitErr, &ee) {
		return ee.ExitCode()
	}
	if p.waitErr != nil {
		return -1
	}
	return 0
}

// FreePort asks the OS for a TCP port nobody listens on. Binding is the only
// honest test on Windows: a port inside a range reserved for Hyper-V looks
// free in every table and still fails to bind.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// NewSecret makes the Clash API secret of one engine run: 16 random bytes.
// It is never stored or shown; a new one is made for every start.
func NewSecret() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
