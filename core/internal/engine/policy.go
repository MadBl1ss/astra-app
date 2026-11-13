package engine

import "time"

// Policy says how hard the supervisor tries to keep a crashing engine up.
type Policy struct {
	// Delays are the pauses before the 1st, 2nd, ... restart in a row. The
	// last one repeats.
	Delays []time.Duration
	// StableAfter is how long a process must run for its crash to count as
	// a fresh problem rather than the next step of the same one.
	StableAfter time.Duration
	// CrashLimit crashes within CrashWindow mean the engine cannot be kept
	// up by restarting: something outside (config, port, driver) is wrong.
	CrashLimit  int
	CrashWindow time.Duration
}

// DefaultPolicy is the one the service ships with. The first pause is
// short because the usual crash is a one-off and the user is without VPN
// while we wait; the growth protects against spinning on a broken setup.
func DefaultPolicy() Policy {
	return Policy{
		Delays:      []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second},
		StableAfter: 60 * time.Second,
		CrashLimit:  5,
		CrashWindow: 2 * time.Minute,
	}
}

// restarts is the memory of one run of the policy. It is a plain value fed
// with times, so it is tested without waiting or processes.
type restarts struct {
	policy  Policy
	step    int
	crashes []time.Time
}

func newRestarts(p Policy) *restarts { return &restarts{policy: p} }

// crashed records a crash at now of a process that had run for uptime. It
// returns the pause before the next start, or loop = true when the limit is
// reached and restarting should stop.
func (r *restarts) crashed(now time.Time, uptime time.Duration) (delay time.Duration, loop bool) {
	if uptime >= r.policy.StableAfter {
		r.step = 0
	}
	keep := r.crashes[:0]
	for _, t := range r.crashes {
		if now.Sub(t) < r.policy.CrashWindow {
			keep = append(keep, t)
		}
	}
	r.crashes = append(keep, now)
	if r.policy.CrashLimit > 0 && len(r.crashes) >= r.policy.CrashLimit {
		return 0, true
	}
	if len(r.policy.Delays) == 0 {
		return 0, false
	}
	i := r.step
	if i >= len(r.policy.Delays) {
		i = len(r.policy.Delays) - 1
	}
	r.step++
	return r.policy.Delays[i], false
}

// count is how many crashes are inside the window right now.
func (r *restarts) count() int { return len(r.crashes) }
