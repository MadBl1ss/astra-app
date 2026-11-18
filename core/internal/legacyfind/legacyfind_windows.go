//go:build windows

package legacyfind

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"strelka/internal/runx"
	"strelka/proto"
)

// candidates lists the folders worth a look. A running old Strelka comes
// first: it is the one in use, while the scheduled task may belong to a copy
// the user has moved on from. Paths of programs started as administrator
// are hidden from a daemon that is not one; the task is then what is left.
func candidates(ctx context.Context) []Candidate {
	var out []Candidate
	for _, p := range running() {
		switch strings.ToLower(filepath.Base(p.path)) {
		case "sing-box-tray.exe", "sing-box.exe":
			out = append(out, Candidate{Dir: filepath.Dir(p.path), Running: true})
		}
	}
	dirs, enabled := readTask(ctx, TaskName)
	for _, dir := range dirs {
		out = append(out, Candidate{Dir: dir, Autostart: enabled})
	}
	return out
}

// taskDirs is what the old setup's task points at; kept for the test that
// reads the real one.
func taskDirs(ctx context.Context) []string {
	dirs, _ := readTask(ctx, TaskName)
	return dirs
}

func schtasks(ctx context.Context, args ...string) ([]byte, error) {
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, err
	}
	return runx.Output(ctx, 10*time.Second, filepath.Join(sys, "schtasks.exe"), "", args...)
}

// readTask returns the folders a task's actions point at and whether it is
// switched on; nothing when there is no such task.
func readTask(ctx context.Context, name string) ([]string, bool) {
	raw, err := schtasks(ctx, "/query", "/tn", name, "/xml", "ONE")
	if err != nil {
		return nil, false // no such task, most likely
	}
	return TaskDirs(raw), TaskEnabled(raw)
}

type process struct {
	pid  uint32
	path string
}

// running lists the programs of interactive sessions with their full paths.
// Session 0 is services: the old Strelka never ran there, and this daemon's
// own engines do.
func running() []process {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var out []process
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		var session uint32
		if windows.ProcessIdToSessionId(e.ProcessID, &session) != nil || session == 0 {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, e.ProcessID)
		if err != nil {
			continue
		}
		path, err := imagePath(h)
		windows.CloseHandle(h)
		if err == nil {
			out = append(out, process{pid: e.ProcessID, path: path})
		}
	}
	return out
}

func imagePath(h windows.Handle) (string, error) {
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:size]), nil
}

// Retire turns the old Strelkas off: it stops every program running from
// one of their folders and switches the logon task off. dirs are the
// folders FindAll returned, never a path a client named. task is
// proto.LegacyTaskDisabled or proto.LegacyTaskAbsent.
func Retire(ctx context.Context, dirs []string) (stopped int, task string, err error) {
	return retire(ctx, dirs, TaskName)
}

func retire(ctx context.Context, dirs []string, taskName string) (int, string, error) {
	in := map[string]bool{}
	for _, d := range dirs {
		in[key(d)] = true
	}
	mine := func(p process) bool { return in[key(filepath.Dir(p.path))] }
	tray := func(p process) bool { return strings.EqualFold(filepath.Base(p.path), "sing-box-tray.exe") }

	// The tray first: it watches its engine and would start it again, and
	// at its own start it kills every sing-box.exe and xray.exe by name.
	stopped := 0
	var errs []error
	for _, pass := range []func(process) bool{tray, func(process) bool { return true }} {
		for _, p := range running() {
			if !mine(p) || !pass(p) {
				continue
			}
			if err := stop(p); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(p.path), err))
				continue
			}
			stopped++
		}
	}

	task, err := disableTask(ctx, taskName)
	if err != nil {
		errs = append(errs, err)
	}
	return stopped, task, errors.Join(errs...)
}

// stop ends one process and waits for it to go. The path is checked again
// on the handle: between the listing and now the id may have been reused.
func stop(p process) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, p.pid)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil // gone already
		}
		return err
	}
	defer windows.CloseHandle(h)
	if path, err := imagePath(h); err != nil || !strings.EqualFold(path, p.path) {
		return nil // not the same process any more
	}
	if err := windows.TerminateProcess(h, 1); err != nil {
		return err
	}
	ev, err := windows.WaitForSingleObject(h, 5000)
	if err != nil {
		return err
	}
	if ev != windows.WAIT_OBJECT_0 {
		return errors.New("не завершилась за 5 секунд")
	}
	return nil
}

// disableTask switches a scheduled task off without deleting it, so that the
// old Strelka can be brought back as it was. Read back afterwards: schtasks
// may succeed on a task it could not actually change.
func disableTask(ctx context.Context, name string) (string, error) {
	if _, err := schtasks(ctx, "/query", "/tn", name); err != nil {
		return proto.LegacyTaskAbsent, nil
	}
	if out, err := schtasks(ctx, "/change", "/tn", name, "/disable"); err != nil {
		return "", fmt.Errorf("задача «%s» не выключилась: %s", name, strings.TrimSpace(string(toUTF8(out))))
	}
	raw, err := schtasks(ctx, "/query", "/tn", name, "/xml", "ONE")
	if err != nil || TaskEnabled(raw) {
		return "", fmt.Errorf("задача «%s» осталась включённой", name)
	}
	return proto.LegacyTaskDisabled, nil
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
