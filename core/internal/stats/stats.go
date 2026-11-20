// Package stats keeps a running picture of every server: how fast it usually
// answers and how often it does not. The numbers come from the test rounds
// the daemon runs through the engine; the window shows them, and the order
// built on them is what a fallback server is picked by.
package stats

import (
	"sort"
	"time"

	"strelka/internal/store"
)

const (
	// How much one new sample moves the running averages.
	weight = 0.2
	// A server that misses every check is rated this many times slower.
	missPenalty = 3.0
	// One miss per server per this long, however often rounds are run: a
	// user hammering "test" must not bury a server that blinked once.
	missEvery = time.Minute
	// Servers gone from every subscription for this long are forgotten.
	keep = 30 * 24 * time.Hour
)

// Stat is one server's history.
type Stat struct {
	Avg    float64   `json:"avg"`  // typical delay, ms
	Miss   float64   `json:"miss"` // share of checks without an answer, 0..1
	N      int       `json:"n"`    // answers seen
	Seen   time.Time `json:"seen"` // the last answer
	MissAt time.Time `json:"miss_at,omitzero"`
}

// Stats is the content of stats.json. It is owned by the daemon loop.
type Stats struct {
	Servers map[string]*Stat `json:"servers"`
}

// Load reads the file; a missing or broken one is an empty picture.
func Load(path string) *Stats {
	st, _ := store.LoadJSON(path, Stats{})
	if st.Servers == nil {
		st.Servers = map[string]*Stat{}
	}
	return &st
}

// Save drops servers nobody has seen for a month and writes the rest.
func (st *Stats) Save(path string, now time.Time) error {
	for tag, s := range st.Servers {
		last := s.Seen
		if s.MissAt.After(last) {
			last = s.MissAt
		}
		if now.Sub(last) > keep {
			delete(st.Servers, tag)
		}
	}
	return store.SaveJSON(path, st)
}

// Answer is how one server did in a test round: OK with the delay and the
// time when it answered, OK false when it did not.
type Answer struct {
	OK bool
	Ms int
	At time.Time
}

// Round folds one test round into the picture. answers holds the servers the
// round got to, whether they answered or not; a server it never reached is
// not in it and is not judged. It reports whether anything changed.
//
// A miss only counts when somebody else in the round answered. With nobody
// answering there is no telling a dead server from a dead network.
func (st *Stats) Round(now time.Time, answers map[string]Answer) bool {
	working := false
	for _, a := range answers {
		if a.OK {
			working = true
			break
		}
	}
	changed := false
	for tag, a := range answers {
		switch {
		case a.OK:
			changed = st.Answered(tag, a.Ms, a.At) || changed
		case working:
			changed = st.Missed(tag, now) || changed
		}
	}
	return changed
}

// Answered folds in one answer of a server. It reports false for a result
// that is already counted.
func (st *Stats) Answered(tag string, ms int, at time.Time) bool {
	s := st.Servers[tag]
	if s != nil && !at.After(s.Seen) {
		return false // the same result as last time
	}
	if s == nil {
		s = &Stat{Avg: float64(ms)}
		st.Servers[tag] = s
	} else {
		s.Avg += weight * (float64(ms) - s.Avg)
		s.Miss -= weight * s.Miss
	}
	s.N++
	s.Seen = at
	return true
}

// Missed records a check a server did not answer. The caller vouches that
// the network was fine: without that a miss says nothing about the server.
func (st *Stats) Missed(tag string, now time.Time) bool {
	s := st.Servers[tag]
	if s == nil {
		s = &Stat{}
		st.Servers[tag] = s
	}
	if now.Sub(s.MissAt) < missEvery {
		return false
	}
	s.Miss += weight * (1 - s.Miss)
	s.MissAt = now
	return true
}

// Rating is what the order is built on: lower is better. A server that never
// answered has none.
func (st *Stats) Rating(tag string) (float64, bool) {
	s := st.Servers[tag]
	if s == nil || s.N == 0 {
		return 0, false
	}
	return s.Avg * (1 + missPenalty*s.Miss), true
}

// Order sorts a pool best first. Servers nobody has measured yet keep their
// place in line behind the measured ones.
func (st *Stats) Order(pool []string) []string {
	out := append([]string(nil), pool...)
	if st == nil {
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, oki := st.Rating(out[i])
		rj, okj := st.Rating(out[j])
		if oki != okj {
			return oki
		}
		return oki && ri < rj
	})
	return out
}
