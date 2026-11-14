//go:build linux

package procs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"strelka/proto"
)

func list() ([]proto.ProcessInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []proto.ProcessInfo
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		dir := filepath.Join("/proc", e.Name())
		fi, err := os.Stat(dir)
		if err != nil {
			continue
		}
		// Regular users start at uid 1000; below that are the system's own.
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Uid < 1000 {
			continue
		}
		path, err := os.Readlink(filepath.Join(dir, "exe"))
		if err != nil {
			continue // kernel threads and processes of other users
		}
		path = strings.TrimSuffix(path, " (deleted)")
		out = append(out, proto.ProcessInfo{Name: filepath.Base(path), Path: path})
	}
	return out, nil
}
