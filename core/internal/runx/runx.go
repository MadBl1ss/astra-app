// Package runx runs an engine binary for a one-off answer: a config check, a
// rule-set match, a version. confgen, xray and the daemon all do this, and
// all need the same three precautions, so they live in one place.
package runx

import (
	"context"
	"os/exec"
	"time"
)

// Output runs exe with args in dir and returns what it printed on both
// streams. It gives up after limit.
//
// The engine is named by its full path and run as that path, never looked up
// in PATH or the working folder: engines are only ever started from the
// engines folder. A daemon without a console (a service) must not flash a
// console window up for each call.
func Output(ctx context.Context, limit time.Duration, exe, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Path = exe
	// exec.Command refuses a program found relative to the current folder;
	// the path here is absolute and chosen by the daemon, so that check is
	// moot and its error is cleared.
	cmd.Err = nil
	cmd.Dir = dir
	hideWindow(cmd)
	return cmd.CombinedOutput()
}
