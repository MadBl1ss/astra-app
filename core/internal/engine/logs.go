package engine

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
)

const (
	// ringLines is how much of an engine's output is kept in memory for the
	// window and for the "why did it die" lines of a crash report.
	ringLines = 500
	// maxLineBytes cuts a runaway line; engines log one event per line.
	maxLineBytes = 8 * 1024
	// logFileLimit is when the log file is rotated. One previous file is
	// kept, so an engine's logs never take more than twice this.
	logFileLimit = 2 << 20
)

// ring keeps the last lines of output.
type ring struct {
	mu    sync.Mutex
	lines []string
	next  int
	full  bool
}

func newRing(n int) *ring { return &ring{lines: make([]string, n)} }

func (r *ring) add(line string) {
	r.mu.Lock()
	r.lines[r.next] = line
	r.next++
	if r.next == len(r.lines) {
		r.next = 0
		r.full = true
	}
	r.mu.Unlock()
}

// tail returns up to n most recent lines, oldest first.
func (r *ring) tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	size := r.next
	if r.full {
		size = len(r.lines)
	}
	if n > size {
		n = size
	}
	out := make([]string, 0, n)
	for i := size - n; i < size; i++ {
		idx := i
		if r.full {
			idx = (r.next + i) % len(r.lines)
		}
		out = append(out, r.lines[idx])
	}
	return out
}

// rotatingFile appends lines to a file and starts a new one when it grows
// past the limit. Errors are swallowed: losing a log line must never take
// the engine down with it.
type rotatingFile struct {
	path  string
	limit int64
	f     *os.File
	size  int64
}

func openRotating(path string, limit int64) *rotatingFile {
	rf := &rotatingFile{path: path, limit: limit}
	rf.open()
	return rf
}

func (rf *rotatingFile) open() {
	f, err := os.OpenFile(rf.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		rf.f = nil
		return
	}
	rf.f = f
	rf.size = 0
	if st, err := f.Stat(); err == nil {
		rf.size = st.Size()
	}
}

func (rf *rotatingFile) writeLine(line string) {
	if rf == nil || rf.f == nil {
		return
	}
	if rf.size+int64(len(line))+1 > rf.limit && rf.size > 0 {
		rf.f.Close()
		os.Remove(rf.path + ".1")
		os.Rename(rf.path, rf.path+".1")
		rf.open()
		if rf.f == nil {
			return
		}
	}
	n, _ := io.WriteString(rf.f, line+"\n")
	rf.size += int64(n)
}

func (rf *rotatingFile) close() {
	if rf != nil && rf.f != nil {
		rf.f.Close()
		rf.f = nil
	}
}

// ansi matches terminal colour sequences. sing-box colours its log levels
// even when stderr is a pipe; the window and the log file want plain text.
var ansi = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// readLines feeds every line of r to sink until EOF. A line longer than
// maxLineBytes is cut and its rest dropped, so one binary blob cannot blow
// the buffers up.
func readLines(r io.Reader, sink func(string)) {
	br := bufio.NewReaderSize(r, maxLineBytes)
	for {
		chunk, isPrefix, err := br.ReadLine()
		if err != nil {
			return
		}
		line := string(chunk)
		for isPrefix && err == nil {
			_, isPrefix, err = br.ReadLine()
		}
		line = strings.TrimRight(ansi.ReplaceAllString(line, ""), "\r \t")
		if line != "" {
			sink(line)
		}
		if err != nil {
			return
		}
	}
}

// Level guesses the level of an engine log line: sing-box and Xray both put
// it as a separate upper-case word near the start. Unknown means "info".
func Level(line string) string {
	head := line
	if len(head) > 64 {
		head = head[:64]
	}
	for _, f := range strings.FieldsFunc(head, func(r rune) bool { return r == ' ' || r == '[' || r == ']' || r == ':' }) {
		switch f {
		case "TRACE", "DEBUG":
			return "debug"
		case "INFO", "Info":
			return "info"
		case "WARN", "WARNING", "Warning":
			return "warn"
		case "ERROR", "Error", "FATAL", "PANIC":
			return "error"
		}
	}
	return "info"
}
