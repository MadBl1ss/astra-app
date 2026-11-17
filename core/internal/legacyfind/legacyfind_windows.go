//go:build windows

package legacyfind

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"strelka/internal/procs"
	"strelka/internal/runx"
)

// candidates lists the folders worth a look. A running old Strelka comes
// first: it is the one in use, while the scheduled task may belong to a copy
// the user has moved on from. Paths of programs started as administrator
// are hidden from a daemon that is not one; the task is then what is left.
func candidates(ctx context.Context) []Candidate {
	var out []Candidate
	if list, err := procs.All(); err == nil {
		for _, p := range list {
			switch strings.ToLower(p.Name) {
			case "sing-box-tray.exe", "sing-box.exe":
				out = append(out, Candidate{Dir: filepath.Dir(p.Path), Running: true})
			}
		}
	}
	for _, dir := range taskDirs(ctx) {
		out = append(out, Candidate{Dir: dir})
	}
	return out
}

func taskDirs(ctx context.Context) []string {
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return nil
	}
	raw, err := runx.Output(ctx, 5*time.Second, filepath.Join(sys, "schtasks.exe"), "",
		"/query", "/tn", TaskName, "/xml", "ONE")
	if err != nil {
		return nil // no such task, most likely
	}
	return TaskDirs(raw)
}

// fromConsole decodes bytes in the console's code page (866 on a Russian
// Windows), which is what a console program prints when it is not told
// otherwise.
func fromConsole(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	// No console (a service): the system's OEM page is the guess.
	cp, err := windows.GetConsoleOutputCP()
	if err != nil || cp == 0 {
		cp = 1 // CP_OEMCP
	}
	n, err := windows.MultiByteToWideChar(cp, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n == 0 {
		return b
	}
	return decode(cp, b, n)
}

func decode(cp uint32, b []byte, n int32) []byte {
	u := make([]uint16, n)
	if _, err := windows.MultiByteToWideChar(cp, 0, &b[0], int32(len(b)), &u[0], n); err != nil {
		return b
	}
	return []byte(windows.UTF16ToString(u))
}
