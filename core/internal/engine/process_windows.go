//go:build windows

package engine

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type platformProcess struct {
	job windows.Handle
}

func lockThreadForChild() {}

// platformStart starts the process inside a Job Object that kills its
// members when the last handle to it closes. The daemon holds the only
// handle, so an engine cannot outlive the daemon, however the daemon dies.
func (p *process) platformStart() error {
	job, err := newKillOnCloseJob()
	if err != nil {
		return fmt.Errorf("job object: %w", err)
	}
	p.cmd.SysProcAttr = &syscall.SysProcAttr{
		// A daemon without a console (a service) would otherwise get a
		// console window for every engine it starts.
		HideWindow: true,
		// Its own group: Ctrl+C in the daemon's console no longer reaches
		// the engine directly, the daemon decides when it stops, and
		// CTRL_BREAK can be aimed at this one process.
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := p.cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return err
	}
	// The process runs for an instant outside the job. Engines do not spawn
	// children, so nothing can escape in that window.
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, h)
		windows.CloseHandle(h)
	}
	if err != nil {
		// An engine we cannot confine is an engine we must not leave running.
		p.cmd.Process.Kill()
		p.cmd.Wait()
		windows.CloseHandle(job)
		return fmt.Errorf("job object: %w", err)
	}
	p.plat.job = job
	return nil
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// softStop asks the engine to shut down cleanly. It works only when the
// daemon and the engine share a console; without one the error tells the
// caller to go straight to kill.
func (p *process) softStop() error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(p.pid))
}

// kill ends everything in the job, the engine and anything it started.
func (p *process) kill() {
	if p.plat.job != 0 {
		windows.TerminateJobObject(p.plat.job, 1)
	}
	p.cmd.Process.Kill()
}

// release closes the job. Called after the process is gone; if anything is
// still in the job, this is what kills it.
func (p *process) release() {
	if p.plat.job != 0 {
		windows.CloseHandle(p.plat.job)
		p.plat.job = 0
	}
}

// FindByPath returns the PIDs of running processes whose image file is
// exactly exe. Matching by name alone is what the old tray did, and on a
// developer's machine that kills the user's working VPN.
func FindByPath(exe string) ([]int, error) {
	var pids []int
	err := eachByPath(exe, windows.PROCESS_QUERY_LIMITED_INFORMATION, func(pid uint32, _ windows.Handle) {
		pids = append(pids, int(pid))
	})
	return pids, err
}

// KillByPath ends every process started from exe, except this one, and
// waits for each to go. It returns how many it ended.
func KillByPath(exe string) (int, error) {
	n := 0
	self := uint32(windows.GetCurrentProcessId())
	access := uint32(windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.PROCESS_TERMINATE | windows.SYNCHRONIZE)
	err := eachByPath(exe, access, func(pid uint32, h windows.Handle) {
		if pid == self {
			return
		}
		// The handle is the one the path was just read from, so the PID
		// cannot have been reused by another program in between.
		if windows.TerminateProcess(h, 1) == nil {
			windows.WaitForSingleObject(h, 3000)
			n++
		}
	})
	return n, err
}

func eachByPath(exe string, access uint32, fn func(pid uint32, h windows.Handle)) error {
	want := normalizePath(exe)
	base := filepath.Base(want)

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snap)

	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		// The name is only a cheap pre-filter; the decision is the full path.
		if !strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), base) {
			continue
		}
		h, err := windows.OpenProcess(access, false, e.ProcessID)
		if err != nil {
			// A process we may not even look at is not ours to judge.
			continue
		}
		if path, err := imagePath(h); err == nil && strings.EqualFold(normalizePath(path), want) {
			fn(e.ProcessID, h)
		}
		windows.CloseHandle(h)
	}
	return nil
}

func imagePath(h windows.Handle) (string, error) {
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:size]), nil
}

// normalizePath makes two spellings of one file compare equal: absolute,
// cleaned, and with 8.3 short names (C:\Users\RUNNER~1\...) expanded.
func normalizePath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	p16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetLongPathName(p16, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return path
	}
	return windows.UTF16ToString(buf[:n])
}

// Alive reports whether a process with this PID is still running.
func Alive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}
