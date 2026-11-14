// Package procs lists the programs the user is running, for the "add a
// program" picker of the rules screen. System processes are left out: nobody
// routes svchost by hand, and the list would drown in them.
package procs

import (
	"sort"
	"strings"

	"strelka/proto"
)

// List returns the running user programs, one entry per file name, sorted
// by name.
func List() ([]proto.ProcessInfo, error) {
	raw, err := list()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []proto.ProcessInfo{}
	for _, p := range raw {
		key := strings.ToLower(p.Name)
		if p.Name == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}
