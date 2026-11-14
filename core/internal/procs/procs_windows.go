//go:build windows

package procs

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"strelka/proto"
)

func list() ([]proto.ProcessInfo, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	// Whatever lives in the Windows folder is the system's own.
	sysRoot := strings.ToLower(os.Getenv("SystemRoot"))
	if sysRoot == "" {
		sysRoot = "c:\\windows"
	}
	sysRoot = strings.TrimRight(sysRoot, "\\") + "\\"

	var out []proto.ProcessInfo
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		// Session 0 is services; a user's programs run in their own session.
		var session uint32
		if windows.ProcessIdToSessionId(e.ProcessID, &session) != nil || session == 0 {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, e.ProcessID)
		if err != nil {
			continue // protected processes are not the user's programs
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		size := uint32(len(buf))
		err = windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
		windows.CloseHandle(h)
		if err != nil {
			continue
		}
		path := windows.UTF16ToString(buf[:size])
		if strings.HasPrefix(strings.ToLower(path), sysRoot) {
			continue
		}
		out = append(out, proto.ProcessInfo{Name: filepath.Base(path), Path: path})
	}
	return out, nil
}
