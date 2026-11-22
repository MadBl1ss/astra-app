// Package stats keeps a running picture of every server: how fast it
// usually answers, how much that wobbles, how often it does not answer at
// all and, once measured, how fast it downloads. The numbers come from the
// daemon's test rounds, the constant probes of the selected server, the
// quiet rounds in the background and the speed tests. The window shows them
// as a score, and the order built on the score is what a fallback server is
// picked by.
//
// Everything is averaged over time, not over samples. A sample's weight
// halves every few days, so last week counts for less than today. Samples
// close together share one unit of weight: the selected server, probed
// every half a minute, does not wash its own history away within the hour,
// and a user pressing "test" five times in a row counts about once.
package stats

import (
	"math"
	"math/bits"
	"sort"
	"time"

	"strelka/internal/store"
)

const (
	// A delay sample, answered or missed, weighs half as much after this.
	halfLife = 72 * time.Hour
	// A speed sample; speed tests are rare, so they are kept longer.
	speedHalfLife = 7 * 24 * time.Hour
	// Evidence nobody has added to for this long is trusted half as much.
	// Slower than halfLife: that one lets new samples take over, this one
	// only says how old the picture is, and an old picture of every server
	// still tells them apart.
	staleHalfLife = 14 * 24 * time.Hour
	// Samples this close share one unit of weight; a sample right after
	// another still weighs minWeight.
	spacing   = 10 * time.Minute
	minWeight = 0.02
	// A slower answer counts as this much: one stuck handshake must not
	// decide the average on its own.
	maxDelay = 3000.0
	// Servers gone from every subscription for this long are forgotten.
	keep = 30 * 24 * time.Hour
	// How many days back "answered on different days" looks.
	dayWindow = 14

	// The priors a server starts from, each worth this much evidence: a
	// wobble of 15% of its delay and one miss in twenty. Without them a
	// server with two lucky answers would look perfect.
	priorWeight = 2.0
	priorDev    = 0.15
	priorLoss   = 0.05
	// How much evidence it takes to trust a score halfway.
	trustWeight = 3.0

	// Version of stats.json. 1 had a moving average and a share of misses.
	version = 2
)

// Stat is one server's history. The sums are as of At; reading them decays
// the weights to the moment asked about.
type Stat struct {
	W      float64   `json:"w"`     // weight of the answers
	Mean   float64   `json:"mean"`  // their weighted mean delay, ms
	Dev    float64   `json:"dev"`   // their weighted mean deviation from it, ms
	MissW  float64   `json:"missW"` // weight of the checks without an answer
	At     time.Time `json:"at"`
	N      int       `json:"n"`    // answers counted, ever
	Seen   time.Time `json:"seen"` // the last answer
	MissAt time.Time `json:"missAt,omitzero"`
	// Days has bit i set when the server answered on day Day-i (days since
	// 1970, UTC): a server that has been good on many days is steadier
	// than one that has been good for an hour.
	Days uint16 `json:"days"`
	Day  int64  `json:"day"`

	// Speed: the weighted mean of ln(Mbit/s), as of SpeedAt. A logarithm,
	// because speeds differ by factors: 10 and 90 average to 30, not 50.
	SW      float64   `json:"sw,omitempty"`
	LnMbps  float64   `json:"lnMbps,omitempty"`
	SpeedAt time.Time `json:"speedAt,omitzero"`
	Last    float64   `json:"lastMbps,omitempty"` // the last speed test, Mbit/s
}

// Stats is the content of stats.json. It is owned by the daemon loop.
type Stats struct {
	Servers map[string]*Stat `json:"servers"`
}

// record is a server as stats.json may hold it: the current fields, and the
// ones version 1 had.
type record struct {
	Stat
	OldAvg    float64   `json:"avg,omitempty"`
	OldMiss   float64   `json:"miss,omitempty"`
	OldMissAt time.Time `json:"miss_at,omitzero"`
}

type file struct {
	Version int                `json:"version"`
	Servers map[string]*record `json:"servers"`
}

// Load reads the file; a missing or broken one is an empty picture. The
// old tray's server-stats.json has the shape of version 1 and loads too.
func Load(path string) *Stats {
	f, _ := store.LoadJSON(path, file{})
	st := &Stats{Servers: make(map[string]*Stat, len(f.Servers))}
	for tag, r := range f.Servers {
		if r == nil {
			continue
		}
		s := r.Stat
		if f.Version < 2 {
			s = upgrade(r)
		}
		st.Servers[tag] = &s
	}
	return st
}

// upgrade turns a version 1 record into the current form. The moving
// average remembered about five samples, so that is the weight it gets;
// its share of misses becomes the weight of misses that gives the same
// share.
func upgrade(r *record) Stat {
	s := Stat{N: r.N, Seen: r.Seen, MissAt: r.OldMissAt}
	if r.N > 0 {
		s.W = math.Min(float64(r.N), 5)
		s.Mean = math.Min(r.OldAvg, maxDelay)
		s.Dev = priorDev * s.Mean
		if r.OldMiss > 0 && r.OldMiss < 1 {
			s.MissW = math.Min(s.W*r.OldMiss/(1-r.OldMiss), 20)
		}
		s.Day = dayOf(r.Seen)
		s.Days = 1
	} else if r.OldMiss > 0 {
		s.MissW = 5 * math.Min(r.OldMiss, 1)
	}
	s.At = r.Seen
	if r.OldMissAt.After(s.At) {
		s.At = r.OldMissAt
	}
	return s
}

// Save drops servers nobody has heard from for a month and writes the rest.
func (st *Stats) Save(path string, now time.Time) error {
	for tag, s := range st.Servers {
		if now.Sub(s.last()) > keep {
			delete(st.Servers, tag)
		}
	}
	return store.SaveJSON(path, struct {
		Version int              `json:"version"`
		Servers map[string]*Stat `json:"servers"`
	}{version, st.Servers})
}

// last is when anything was last learnt about the server.
func (s *Stat) last() time.Time {
	t := s.Seen
	for _, u := range []time.Time{s.MissAt, s.SpeedAt} {
		if u.After(t) {
			t = u
		}
	}
	return t
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

// decay brings the delay sums to t. It never runs the clock back: a sample
// from a moment ago weighs as if it came now.
func (s *Stat) decay(t time.Time) {
	if !t.After(s.At) {
		return
	}
	if !s.At.IsZero() {
		f := halve(t.Sub(s.At), halfLife)
		s.W *= f
		s.MissW *= f
	}
	s.At = t
}

func halve(d, half time.Duration) float64 {
	return math.Exp2(-float64(d) / float64(half))
}

// weight is what a sample at t is worth, given the last one before it.
func (s *Stat) weight(t time.Time) float64 {
	prev := s.Seen
	if s.MissAt.After(prev) {
		prev = s.MissAt
	}
	if prev.IsZero() {
		return 1
	}
	return math.Max(minWeight, math.Min(1, float64(t.Sub(prev))/float64(spacing)))
}

func dayOf(t time.Time) int64 { return t.Unix() / 86400 }

// Answered folds in one answer of a server. It reports false for a result
// that is already counted.
func (st *Stats) Answered(tag string, ms int, at time.Time) bool {
	s := st.Servers[tag]
	if s != nil && !at.After(s.Seen) {
		return false // the same result as last time
	}
	if s == nil {
		s = &Stat{}
		st.Servers[tag] = s
	}
	w := s.weight(at)
	s.decay(at)
	x := math.Min(math.Max(float64(ms), 1), maxDelay)
	if s.W <= 0 {
		s.Mean, s.Dev = x, 0
	} else {
		s.Dev = (s.W*s.Dev + w*math.Abs(x-s.Mean)) / (s.W + w)
		s.Mean = (s.W*s.Mean + w*x) / (s.W + w)
	}
	s.W += w
	s.N++
	s.Seen = at
	s.markDay(at)
	return true
}

func (s *Stat) markDay(t time.Time) {
	day := dayOf(t)
	switch shift := day - s.Day; {
	case s.Days == 0 || shift >= 16:
		s.Days = 1
	case shift > 0:
		s.Days = s.Days<<shift | 1
	case shift > -16:
		s.Days |= 1 << -shift
		return // an older day: the window stays where it is
	default:
		return
	}
	s.Day = day
}

// Missed records a check a server did not answer. The caller vouches that
// the network was fine: without that a miss says nothing about the server.
func (st *Stats) Missed(tag string, now time.Time) bool {
	s := st.Servers[tag]
	if s == nil {
		s = &Stat{}
		st.Servers[tag] = s
	}
	if !now.After(s.MissAt) || !now.After(s.Seen) {
		return false
	}
	w := s.weight(now)
	s.decay(now)
	s.MissW += w
	s.MissAt = now
	return true
}

// Speed folds in one speed test. Anything below a tenth of a megabit is a
// tenth: the logarithm of nothing is no number.
func (st *Stats) Speed(tag string, mbps float64, at time.Time) bool {
	s := st.Servers[tag]
	if s == nil {
		s = &Stat{}
		st.Servers[tag] = s
	}
	if !at.After(s.SpeedAt) {
		return false
	}
	x := math.Log(math.Max(mbps, 0.1))
	sw := 0.0
	if !s.SpeedAt.IsZero() {
		sw = s.SW * halve(at.Sub(s.SpeedAt), speedHalfLife)
	}
	s.LnMbps = (sw*s.LnMbps + x) / (sw + 1)
	s.SW = sw + 1
	s.SpeedAt = at
	s.Last = mbps
	return true
}

// Quality is a server as the window and the fallback order see it now.
type Quality struct {
	Known  bool    // anything at all was learnt about the server
	Ping   float64 // typical delay, ms; 0 when it never answered
	Jitter float64 // how much the delay wobbles around that, ms
	Loss   float64 // share of checks without an answer, 0..1
	Mbps   float64 // typical download speed; 0 when never measured
	Score  float64 // 0..100, higher is better
	Trust  float64 // 0..1: how much of the score is evidence, not prior
	Days   int     // days with answers in the last two weeks
	Stable bool    // has answered on several days, nearly always, evenly
}

// Quality reads a server's history as of now.
func (st *Stats) Quality(tag string, now time.Time) Quality {
	if st == nil {
		return Quality{}
	}
	s := st.Servers[tag]
	if s == nil {
		return Quality{}
	}
	f := 1.0
	if now.After(s.At) && !s.At.IsZero() {
		f = halve(now.Sub(s.At), staleHalfLife)
	}
	w, miss := s.W*f, s.MissW*f
	q := Quality{Known: s.N > 0 || s.MissW > 0 || s.SW > 0}
	if !q.Known {
		return q
	}
	if w+miss > 0 {
		q.Loss = miss / (w + miss)
	}
	if s.N > 0 {
		q.Ping = s.Mean
		// With little evidence the wobble is assumed, not measured.
		q.Jitter = (w*s.Dev + priorWeight*priorDev*s.Mean) / (w + priorWeight)
	}
	if s.SW > 0 {
		q.Mbps = math.Exp(s.LnMbps)
	}
	if s.Days != 0 {
		age := dayOf(now) - s.Day
		if age >= 0 && age < dayWindow {
			q.Days = bits.OnesCount16(s.Days & (1<<(dayWindow-age) - 1))
		}
	}

	// Each part is 0..1. Delay: what one gets nearly always, the typical
	// delay and twice its wobble; 300 ms is half marks.
	lat := 0.0
	if s.N > 0 {
		l := (q.Ping + 2*q.Jitter) / 300
		lat = 1 / (1 + l*l)
	}
	// Answers: misses are what one notices, so they cost the most.
	loss := (miss + priorWeight*priorLoss) / (w + miss + priorWeight)
	rel := math.Pow(1-loss, 4)
	// Speed: 15 Mbit/s is half marks. Never measured is taken for 22,
	// which neither rewards nor punishes a server that was not asked.
	mbps := q.Mbps
	if mbps == 0 {
		mbps = 22
	}
	spd := mbps / (mbps + 15)
	raw := math.Pow(lat, 0.45) * rel * math.Pow(spd, 0.35)

	// The score leans towards the middle until there is evidence for it:
	// one lucky answer does not make a server the best one.
	evidence := w + miss + math.Min(s.SW, 2)
	q.Trust = evidence / (evidence + trustWeight)
	q.Score = 100 * (q.Trust*raw + (1-q.Trust)*0.5)

	q.Stable = s.N > 0 && w >= 6 && q.Days >= 3 && q.Loss < 0.03 && q.Jitter <= math.Max(25, 0.3*q.Ping)
	return q
}

// Order sorts a pool best first as of now. Servers nobody has measured yet
// keep their place in line behind the measured ones, and so do servers that
// have never answered at all.
func (st *Stats) Order(pool []string, now time.Time) []string {
	out := append([]string(nil), pool...)
	if st == nil {
		return out
	}
	score := make(map[string]float64, len(out))
	rated := make(map[string]bool, len(out))
	for _, tag := range out {
		if s := st.Servers[tag]; s != nil && s.N > 0 {
			score[tag], rated[tag] = st.Quality(tag, now).Score, true
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rated[out[i]], rated[out[j]]
		if ri != rj {
			return ri
		}
		return ri && score[out[i]] > score[out[j]]
	})
	return out
}
