package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

// logLimit is where the service's log starts over. One previous file is
// kept beside it, so a support report always has the last few days.
const logLimit = 2 << 20

// logFile is the service's own log. A service has no console and no stdout:
// without a file there is nowhere to learn why it did not start.
type logFile struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func openLog(path string) (*logFile, error) {
	l := &logFile{path: path}
	if err := l.reopen(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *logFile) reopen() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, st.Size()
	// A panic in the service would otherwise go to a stderr nobody reads.
	debug.SetCrashOutput(f, debug.CrashOptions{})
	return nil
}

// Printf writes one line. It has the shape of daemon.Options.Logf.
func (l *logFile) Printf(format string, a ...any) {
	line := fmt.Sprintf("%s  %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, a...))
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return
	}
	if l.size+int64(len(line)) > logLimit {
		l.rotate()
	}
	n, _ := l.f.WriteString(line)
	l.size += int64(n)
}

// rotate moves the full file aside. Windows will not rename an open file,
// so it is closed first; if the reopen fails the log goes quiet rather than
// taking the service down.
func (l *logFile) rotate() {
	// The runtime keeps its own handle to the crash output, and that handle
	// alone would keep the rename from happening.
	debug.SetCrashOutput(nil, debug.CrashOptions{})
	l.f.Close()
	l.f = nil
	old := l.path + ".1"
	os.Remove(old)
	os.Rename(l.path, old)
	l.reopen()
}

func (l *logFile) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		debug.SetCrashOutput(nil, debug.CrashOptions{})
		l.f.Close()
		l.f = nil
	}
}
