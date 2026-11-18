//go:build !windows

package legacyfind

import "context"

// The old Strelka only ever ran on Windows.
func candidates(context.Context) []Candidate { return nil }

func Retire(context.Context, []string) (int, string, error) { return 0, "", ErrUnsupported }

func fromConsole(b []byte) []byte { return b }
