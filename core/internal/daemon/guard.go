package daemon

import (
	"errors"
	"os"
	"time"

	"strelka/internal/confgen"
	"strelka/internal/guard"
	"strelka/proto"
)

// This file is where the kill switch meets the daemon. The rule it keeps is
// The kill switch in one line: while the VPN is meant to be up, the guard
// is engaged - before the engine starts, after it stops, and all the time it
// is down in between.
//
// There is no sequence to follow. syncGuard compares what should be with
// what is and closes the gap; the loop calls it after every message and on
// every tick, so a missed event costs a second, not a leak.

const (
	// guardRetryEvery paces the attempts to engage a guard the system has
	// refused: the reason (no rights, the filter engine is down) does not go
	// away by itself within a tick.
	guardRetryEvery = 30 * time.Second
	// tunnelLookEvery and tunnelLookFor: how the adapter is looked for right
	// after the engine starts. Until its permit is in, everything that goes
	// through the tunnel hits the final block, so this is not left to the
	// tick.
	tunnelLookEvery = 50 * time.Millisecond
	tunnelLookFor   = 15 * time.Second
)

// guardState is what the loop remembers about the kill switch.
type guardState struct {
	on      bool       // Engage succeeded and Release has not been called since
	spec    guard.Spec // what it is engaged with
	retryAt time.Time  // no new attempt to engage before this
	told    bool       // the journal already says it would not engage
	tun     uint64     // the adapter last given to the guard
	boxUp   bool       // sing-box is running: the adapter may exist
	lookGen uint64     // cancels a tunnel lookup that has been overtaken
	// downSince: the engine has been missing since then while the guard was
	// engaged; zero when it is not missing.
	downSince time.Time
}

// guardWanted: the setting is on and the VPN is anything but off. "failed"
// counts - an engine that will not start is exactly when the traffic would
// otherwise go around it.
func (d *Daemon) guardWanted() bool {
	return d.opts.Guard != nil && d.st.Settings.Guard && d.st.VPN.Phase != proto.PhaseOff
}

func (d *Daemon) guardSpec() guard.Spec {
	apps := []string{d.opts.Paths.Engine(boxName), d.opts.Paths.Engine(xrayName)}
	if self, err := os.Executable(); err == nil {
		apps = append(apps, self)
	}
	return guard.Spec{Apps: apps, AllowLAN: d.st.Settings.AllowLAN}
}

func sameSpec(a, b guard.Spec) bool {
	if a.AllowLAN != b.AllowLAN || len(a.Apps) != len(b.Apps) {
		return false
	}
	for i := range a.Apps {
		if a.Apps[i] != b.Apps[i] {
			return false
		}
	}
	return true
}

// syncGuard brings the guard to what the state asks for. now is passed in so
// that a call from tick and the judge that follows agree on the time.
// force skips the pause after a refusal: the user has just asked for the VPN.
func (d *Daemon) syncGuard(now time.Time, force bool) {
	g := d.opts.Guard
	if g == nil {
		return
	}
	gs := &d.gs
	switch want := d.guardWanted(); {
	case !want && gs.on:
		if err := g.Release(); err != nil {
			d.opts.Logf("guard: release: %v", err)
		}
		d.opts.Logf("guard: released")
		gs.on, gs.told, gs.retryAt = false, false, time.Time{}

	case !want:
		gs.told, gs.retryAt = false, time.Time{}

	case want:
		spec := d.guardSpec()
		if gs.on && sameSpec(spec, gs.spec) {
			break
		}
		if !force && now.Before(gs.retryAt) {
			break
		}
		if err := g.Engage(spec); err != nil {
			gs.retryAt = now.Add(guardRetryEvery)
			d.opts.Logf("guard: engage: %v", err)
			// The VPN runs all the same: refusing to start it would leave the
			// user with neither. On a system without a guard yet (Linux until
			// stage 09) there is nothing to tell either.
			if !gs.on && !gs.told && !errors.Is(err, guard.ErrUnsupported) {
				gs.told = true
				d.log(proto.CatGuard, proto.LevelProblem,
					"Замок не включился: сетевой экран Windows отказал. VPN работает без него, трафик при сбое может пойти мимо.")
			}
			break
		}
		if !gs.on {
			d.opts.Logf("guard: engaged, %d filters", g.Status().Filters)
			if gs.told {
				d.log(proto.CatGuard, proto.LevelInfo, "Замок включился.")
			}
		}
		gs.on, gs.spec, gs.told, gs.retryAt = true, spec, false, time.Time{}
		if gs.boxUp {
			d.lookForTunnel()
		}
	}
	d.syncTunnel()
	d.syncBlocking(now)
}

// syncTunnel keeps the guard's permit on the adapter that exists now. The
// adapter comes with the engine and gets a new id with every start.
func (d *Daemon) syncTunnel() {
	gs := &d.gs
	var luid uint64
	if gs.on && gs.boxUp {
		luid, _ = d.findTunnel()
	}
	d.setTunnel(luid)
}

func (d *Daemon) findTunnel() (uint64, bool) {
	if d.opts.FindTunnel != nil {
		return d.opts.FindTunnel()
	}
	return guard.TunnelLUID(confgen.TUNName)
}

func (d *Daemon) setTunnel(luid uint64) {
	if d.opts.Guard == nil || luid == d.gs.tun {
		return
	}
	d.gs.tun = luid
	if err := d.opts.Guard.SetTunnel(luid); err != nil {
		d.opts.Logf("guard: tunnel permit for %#x: %v", luid, err)
		return
	}
	d.opts.Logf("guard: tunnel permit moved to %#x", luid)
}

// lookForTunnel polls for the adapter off the loop and hands the answer
// back. The tick would find it too, a second later; a second of "the VPN is
// on and nothing loads" is what this saves.
func (d *Daemon) lookForTunnel() {
	d.gs.lookGen++
	gen := d.gs.lookGen
	go func() {
		deadline := time.Now().Add(tunnelLookFor)
		for time.Now().Before(deadline) {
			if luid, ok := d.findTunnel(); ok {
				d.post(func() {
					if gen == d.gs.lookGen && d.gs.on && d.gs.boxUp {
						d.setTunnel(luid)
					}
				})
				return
			}
			select {
			case <-d.done:
				return
			case <-time.After(tunnelLookEvery):
			}
		}
	}()
}

// engineUp and engineDown are told by the loop when sing-box starts and
// when it is gone, whatever the reason.
func (d *Daemon) engineUp() {
	d.gs.boxUp = true
	if d.gs.on {
		d.lookForTunnel()
	}
}

func (d *Daemon) engineDown() {
	d.gs.boxUp = false
	d.gs.lookGen++
	d.setTunnel(0)
}

// syncBlocking works out State.guard. "Blocking" is not "engaged and the
// engine is down" - that is every restart - but that having lasted.
func (d *Daemon) syncBlocking(now time.Time) {
	gs := &d.gs
	down := false
	if gs.on && !d.boxReady {
		switch d.st.VPN.Phase {
		case proto.PhaseStarting, proto.PhaseRecovering, proto.PhaseFailed:
			down = true
		}
	}
	switch {
	case !down:
		gs.downSince = time.Time{}
	case gs.downSince.IsZero():
		gs.downSince = now
	}
	blocking := down && now.Sub(gs.downSince) >= d.tune.BlockAfter
	if d.st.Guard.Engaged == gs.on && d.st.Guard.Blocking == blocking {
		return
	}
	if blocking != d.st.Guard.Blocking {
		d.opts.Logf("guard: blocking: %v", blocking)
	}
	d.st.Guard.Engaged, d.st.Guard.Blocking = gs.on, blocking
	d.touch()
	// The incidents word themselves differently while the internet is closed.
	d.judge(now)
}

func (d *Daemon) guardStatus() proto.GuardStatusResult {
	if d.opts.Guard == nil {
		return proto.GuardStatusResult{}
	}
	st := d.opts.Guard.Status()
	return proto.GuardStatusResult{Engaged: st.Engaged, Filters: st.Filters, TunLUID: st.TunLUID}
}

// releaseGuard is the last thing a daemon does to the guard: the engines are
// stopped, the process is about to go.
func (d *Daemon) releaseGuard() {
	if d.opts.Guard == nil || !d.gs.on {
		return
	}
	if err := d.opts.Guard.Release(); err != nil {
		d.opts.Logf("guard: release: %v", err)
	}
	d.gs.on = false
}
