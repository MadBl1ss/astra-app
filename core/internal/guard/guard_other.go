//go:build !windows

package guard

// The Linux kill switch (nftables) is stage 09. Until then the guard reports
// itself unsupported and the daemon runs without one.

type unsupported struct{}

// New returns the kill switch of this system. logf may be nil.
func New(logf func(format string, a ...any)) Guard { return unsupported{} }

func (unsupported) Engage(Spec) error      { return ErrUnsupported }
func (unsupported) SetTunnel(uint64) error { return nil }
func (unsupported) Release() error         { return nil }
func (unsupported) Status() Status         { return Status{} }

// Reset removes everything the guard may have left in the system.
func Reset() (int, error) { return 0, ErrUnsupported }

// TunnelLUID finds the adapter with this name.
func TunnelLUID(alias string) (luid uint64, ok bool) { return 0, false }
