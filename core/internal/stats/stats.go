// Package stats keeps a running picture of every server: how fast it usually
// answers and how often it does not. The numbers come from the test rounds
// the daemon runs through the engine; the window shows them, and the order
// built on them is what a fallback server is picked by.
package stats

import (
	"sort"
	"time"

	"strelka/internal/clash"
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

// Round folds one finished test round into the picture. started is when the
// round began: a result older than that is left over from an earlier round
// and says nothing about this one. It reports whether anything changed.
//
// A miss only counts when somebody else in the round answered. With nobody
// answering there is no telling a dead server from a dead network.
func (st *Stats) Round(started, now time.Time, pool []string, px map[string]clash.ProxyInfo) bool {
	answered := func(p clash.ProxyInfo) bool { return p.Alive() && !p.Tested.Before(started) }
	working := false
	for _, tag := range pool {
		if answered(px[tag]) {
			working = true
			break
		}
	}
	changed := false
	for _, tag := range pool {
		p, known := px[tag]
		if !known {
			continue
		}
		s := st.Servers[tag]
		switch {
		case answered(p):
			if s != nil && !p.Tested.After(s.Seen) {
				continue // the same result as last time
			}
			if s == nil {
				s = &Stat{Avg: float64(p.Delay)}
				st.Servers[tag] = s
			} else {
				s.Avg += weight * (float64(p.Delay) - s.Avg)
				s.Miss -= weight * s.Miss
			}
			s.N++
			s.Seen = p.Tested
			changed = true
		case working:
			if s == nil {
				s = &Stat{}
				st.Servers[tag] = s
			}
			if now.Sub(s.MissAt) < missEvery {
				continue
			}
			s.Miss += weight * (1 - s.Miss)
			s.MissAt = now
			changed = true
		}
	}
	return changed
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
