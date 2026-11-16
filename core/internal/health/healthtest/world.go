// Package healthtest is the stand for scenario tests of the health automata:
// a fake clock, and a network described by a few switches that answers the
// probes a Machine asks for. Whole hours of trouble run in microseconds.
package healthtest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"strelka/internal/health"
	"strelka/proto"
)

// World plays the daemon and everything beyond it for a Machine: it ticks
// the clock once a second, runs the probes and hands the answers back when
// they would have arrived.
type World struct {
	T   *testing.T
	M   *health.Machine
	Now time.Time
	T0  time.Time

	ServerUp  bool
	Internet  bool
	Others    []string      // the other servers that answer
	Ping      int           // ms, a live server's answer
	MissTakes time.Duration // how long a dead server takes to fail a probe

	NextTick time.Time
	Pending  []Arrival
	Probes   []Sent    // every server probe, in order
	Asked    []string  // "direct" and "others", in order
	Changes  []Verdict // every change of the verdict

	// OnStep, when set, is called after every event with the diagnosis it
	// left: the place to hang whatever consumes diagnoses.
	OnStep func(now time.Time, d health.Diagnosis)
}

// Arrival is a probe's answer on its way back.
type Arrival struct {
	At time.Time
	Ev health.Event
}

// Sent is one server probe.
type Sent struct {
	At  time.Duration // since the start of the test
	URL string
}

// Verdict is one change of the verdict.
type Verdict struct {
	At time.Duration
	V  string
}

// NewWorld is a world with the VPN off, a network and the server a-DE selected.
func NewWorld(t *testing.T) *World {
	t0 := time.Unix(1_800_000_000, 0).UTC()
	w := &World{
		T: t, M: health.New(health.Default()), Now: t0, T0: t0, NextTick: t0.Add(time.Second),
		ServerUp: true, Internet: true, Others: []string{"b-NL", "c-FI"}, Ping: 80,
		MissTakes: 5 * time.Second,
	}
	w.Send(health.Network{Up: true})
	w.Send(health.Select{Server: "a-DE"})
	return w
}

// Started is a world whose VPN is on and has answered its first probe.
func Started(t *testing.T) *World {
	w := NewWorld(t)
	w.Send(health.Phase{Phase: proto.PhaseOn})
	w.Run(2 * time.Second)
	w.Want(proto.VerdictOK)
	return w
}

// Send feeds one event to the machine at the present moment.
func (w *World) Send(ev health.Event) {
	w.T.Helper()
	for _, c := range w.M.Step(w.Now, ev) {
		w.exec(c)
	}
	d := w.M.Diagnosis()
	if n := len(w.Changes); n == 0 || w.Changes[n-1].V != d.Verdict {
		w.Changes = append(w.Changes, Verdict{At: w.Now.Sub(w.T0), V: d.Verdict})
	}
	if w.OnStep != nil {
		w.OnStep(w.Now, d)
	}
}

func (w *World) exec(c health.Command) {
	later := func(d time.Duration, ev health.Event) {
		w.Pending = append(w.Pending, Arrival{At: w.Now.Add(d), Ev: ev})
	}
	switch c := c.(type) {
	case health.ProbeServer:
		w.Probes = append(w.Probes, Sent{At: w.Now.Sub(w.T0), URL: c.URL})
		if w.ServerUp && w.Internet {
			later(time.Duration(w.Ping)*time.Millisecond, health.ServerResult{Gen: c.Gen, OK: true, Ms: w.Ping})
		} else {
			later(w.MissTakes, health.ServerResult{Gen: c.Gen})
		}
	case health.ProbeDirect:
		w.Asked = append(w.Asked, "direct")
		if w.Internet {
			later(60*time.Millisecond, health.DirectResult{Gen: c.Gen, OK: true})
		} else {
			later(2*w.MissTakes, health.DirectResult{Gen: c.Gen}) // both addresses were tried
		}
	case health.ProbeOthers:
		w.Asked = append(w.Asked, "others")
		alive := w.Others
		if !w.Internet {
			alive = nil
		}
		wait := 200 * time.Millisecond
		if len(alive) == 0 {
			wait = w.MissTakes
		}
		later(wait, health.OthersResult{Gen: c.Gen, Alive: alive, Tested: 5})
	}
}

// Run moves the clock forward by d, delivering ticks and probe answers in
// the order they fall due.
func (w *World) Run(d time.Duration) {
	w.T.Helper()
	end := w.Now.Add(d)
	for {
		first := -1
		for i, p := range w.Pending {
			if !p.At.After(end) && !p.At.After(w.NextTick) && (first < 0 || p.At.Before(w.Pending[first].At)) {
				first = i
			}
		}
		if first >= 0 {
			p := w.Pending[first]
			w.Pending = append(w.Pending[:first], w.Pending[first+1:]...)
			if p.At.After(w.Now) {
				w.Now = p.At
			}
			w.Send(p.Ev)
			continue
		}
		if w.NextTick.After(end) {
			w.Now = end
			return
		}
		w.Now = w.NextTick
		w.NextTick = w.NextTick.Add(time.Second)
		w.Send(health.Tick{})
	}
}

// Traffic runs the clock with bytes coming back through the VPN every two
// seconds, the way the daemon reports them.
func (w *World) Traffic(d time.Duration) {
	w.T.Helper()
	for end := w.Now.Add(d); w.Now.Before(end); {
		w.Run(2 * time.Second)
		w.Send(health.Traffic{})
	}
}

// Sleep stops the world for d, as a closed lid does: no ticks, and nothing
// that was on its way arrives. The machine is told on waking.
func (w *World) Sleep(d time.Duration) {
	w.T.Helper()
	w.Pending = nil
	w.Now = w.Now.Add(d)
	w.NextTick = w.Now.Add(time.Second)
	w.Send(health.Wake{})
}

func (w *World) Verdict() string { return w.M.Diagnosis().Verdict }

// Want fails the test unless the verdict is v.
func (w *World) Want(v string) {
	w.T.Helper()
	if got := w.Verdict(); got != v {
		w.T.Fatalf("через %v диагноз %q, жду %q (как менялся: %s)", w.Elapsed(), got, v, w.History())
	}
}

// History is every verdict so far with the time it came.
func (w *World) History() string {
	var parts []string
	for _, c := range w.Changes {
		parts = append(parts, fmt.Sprintf("%v %s", c.At, c.V))
	}
	return strings.Join(parts, " → ")
}

// Until runs the clock until the verdict is v and returns how long it took.
func (w *World) Until(v string, limit time.Duration) time.Duration {
	w.T.Helper()
	from := w.Now
	for w.Verdict() != v {
		if w.Now.Sub(from) >= limit {
			w.T.Fatalf("за %v диагноз так и не стал %q (как менялся: %s)", limit, v, w.History())
		}
		w.Run(100 * time.Millisecond)
	}
	return w.Now.Sub(from)
}

// Saw reports whether the verdict became v at or after the given moment.
func (w *World) Saw(v string, since time.Duration) bool {
	for _, c := range w.Changes {
		if c.V == v && c.At >= since {
			return true
		}
	}
	return false
}

// Elapsed is the time since the start of the test.
func (w *World) Elapsed() time.Duration { return w.Now.Sub(w.T0) }
