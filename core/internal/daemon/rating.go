package daemon

import (
	"context"
	"sort"
	"sync"
	"time"

	"strelka/internal/clash"
	"strelka/internal/stats"
	"strelka/proto"
)

// Quiet rounds: while the VPN works, every so often a handful of servers is
// pinged through the engine, so that the rating learns about the servers
// nobody is using and about every hour of the day, without anybody pressing
// "test". A ping is a few kilobytes; the round goes nowhere near the
// selected server, which the health machine probes anyway.
const (
	rateEvery   = 45 * time.Minute
	rateSettle  = 3 * time.Minute // after the VPN has come on
	rateRetry   = 5 * time.Minute // after a round had to wait
	rateMax     = 12
	rateWorkers = 3
	// Above this many bytes a second through the engine the line is busy
	// with somebody's download, and every ping would be about that.
	rateBusyDown = 1 << 20
)

// rateState is the loop's memory of the quiet rounds.
type rateState struct {
	next    time.Time // when the next round is due; zero: not planned
	last    time.Time // when the last one ended
	running bool
}

func (d *Daemon) rateTimes() (every, settle time.Duration) {
	every, settle = rateEvery, rateSettle
	if d.opts.RateEvery > 0 {
		every = d.opts.RateEvery
	}
	if d.opts.RateSettle > 0 {
		settle = d.opts.RateSettle
	}
	return every, settle
}

// autoRate runs on every tick and starts a round when one is due.
func (d *Daemon) autoRate(now time.Time) {
	rs := &d.rate
	if !d.st.Settings.Rating || d.st.VPN.Phase != proto.PhaseOn || !d.live.Load() {
		rs.next = time.Time{}
		return
	}
	every, settle := d.rateTimes()
	if rs.next.IsZero() {
		// Some time for the VPN to settle, and no sooner than the round
		// after the last one, whatever the VPN did meanwhile.
		rs.next = now.Add(settle)
		if due := rs.last.Add(every); due.After(rs.next) {
			rs.next = due
		}
	}
	if rs.running || now.Before(rs.next) {
		return
	}
	if d.job != "" || d.diagBusy || d.st.Health.Verdict != proto.VerdictOK || d.downRate.Load() > rateBusyDown {
		rs.next = now.Add(min(rateRetry, every))
		return
	}
	api := d.api.Load()
	pool := d.ratePool(now)
	if api == nil || len(pool) == 0 {
		rs.next = now.Add(every)
		return
	}
	rs.running = true
	go func() {
		answers := quietRound(api, pool)
		d.post(func() { d.rateDone(answers) })
	}()
}

// quietRound pings the pool a few servers at a time. A server the engine
// could not be asked about is left out of the answers: that says nothing
// about the server.
func quietRound(api *clash.Client, pool []string) map[string]stats.Answer {
	answers := make(map[string]stats.Answer, len(pool))
	var mu sync.Mutex
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range min(rateWorkers, len(pool)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				ms, err := api.Delay(context.Background(), id)
				if err != nil && clash.Unreachable(err) {
					continue
				}
				mu.Lock()
				answers[id] = stats.Answer{OK: err == nil, Ms: ms, At: time.Now()}
				mu.Unlock()
			}
		}()
	}
	for _, id := range pool {
		jobs <- id
	}
	close(jobs)
	wg.Wait()
	return answers
}

func (d *Daemon) rateDone(answers map[string]stats.Answer) {
	now := time.Now()
	every, _ := d.rateTimes()
	d.rate.running, d.rate.last, d.rate.next = false, now, now.Add(every)
	alive := 0
	for id, a := range answers {
		if d.prof.Server(id) == nil {
			delete(answers, id) // removed meanwhile
			continue
		}
		if a.OK {
			alive++
			d.pings[id] = ping{ms: a.Ms, at: a.At, alive: true}
		} else {
			d.pings[id] = ping{at: a.At}
		}
	}
	d.statsDirty = d.stats.Round(now, answers) || d.statsDirty
	d.opts.Logf("quiet round: %d of %d answered", alive, len(answers))
	d.refresh()
	d.touch()
}

// ratePool picks a round's servers: the favourites, which are the fallback
// and must be known best; then the best of the rest, which the window puts
// on top; then the ones heard of least recently, never tested first.
// Servers of a subscription that has run out are left alone.
func (d *Daemon) ratePool(now time.Time) []string {
	pool := make([]string, 0, rateMax)
	taken := map[string]bool{d.prof.Selected: true}
	add := func(id string) {
		if len(pool) < rateMax && !taken[id] && d.prof.Server(id) != nil && !d.serverExpired(id, now) {
			taken[id] = true
			pool = append(pool, id)
		}
	}
	for i, id := range d.prof.Favorites {
		if i == 6 {
			break
		}
		add(id)
	}
	all := make([]string, 0, len(d.prof.Servers))
	for _, s := range d.prof.Servers {
		all = append(all, s.ID)
	}
	best := 0
	for _, id := range d.stats.Order(all, now) {
		if best == 4 {
			break
		}
		if s := d.stats.Servers[id]; s != nil && s.N > 0 && !taken[id] {
			add(id)
			best++
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		return d.stats.LastHeard(all[i]).Before(d.stats.LastHeard(all[j]))
	})
	for _, id := range all {
		add(id)
	}
	return pool
}
