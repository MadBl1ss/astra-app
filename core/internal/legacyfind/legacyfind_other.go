//go:build !windows

package legacyfind

import "context"

// The old Strelka only ever ran on Windows.
func candidates(context.Context) []Candidate { return nil }

func fromConsole(b []byte) []byte { return b }
