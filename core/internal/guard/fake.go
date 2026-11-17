package guard

import (
	"fmt"
	"sync"
)

// Fake is a guard for the daemon's tests: it touches nothing and remembers
// what it was told, in order.
type Fake struct {
	// Err, when set, is what Engage answers: a system that refuses filters.
	Err error

	mu      sync.Mutex
	calls   []string
	engaged bool
	spec    Spec
	tun     uint64
}

func (f *Fake) Engage(spec Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		f.calls = append(f.calls, "engage-failed")
		return f.Err
	}
	f.calls = append(f.calls, fmt.Sprintf("engage lan=%v apps=%d", spec.AllowLAN, len(spec.Apps)))
	f.engaged, f.spec = true, spec
	return nil
}

func (f *Fake) SetTunnel(luid uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("tunnel %d", luid))
	f.tun = luid
	return nil
}

func (f *Fake) Release() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "release")
	f.engaged = false
	return nil
}

func (f *Fake) Status() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := Status{Engaged: f.engaged}
	if f.engaged {
		st.TunLUID = f.tun
		for _, r := range Build(f.spec) {
			st.Filters += len(r.Layers())
		}
		if f.tun != 0 {
			st.Filters += len(TunnelRule().Layers())
		}
	}
	return st
}

// Calls returns what the guard was told so far.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
