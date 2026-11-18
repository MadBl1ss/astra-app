package daemon

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"strelka/internal/clash"
	"strelka/internal/confgen"
	"strelka/internal/health"
	"strelka/internal/incident"
	"strelka/internal/ipc"
	"strelka/internal/netmon"
	"strelka/internal/store"
	"strelka/proto"
)

// This file is where the health and incident automata meet the world. They
// are pure: here their questions become requests to the engine, and their
// answers become State.health, State.incidents, journal lines and toasts.
// Everything runs in the loop; only the probes themselves leave it.

// feed gives the health machine one event, sends the probes it asks for and
// applies the verdict it arrives at.
func (d *Daemon) feed(ev health.Event) {
	now := time.Now()
	for _, c := range d.hm.Step(now, ev) {
		d.probe(c)
	}
	d.judge(now)
}

// acted notes that the user has just done something. What breaks in the
// next moments is that, not an incident.
func (d *Daemon) acted() { d.im.UserAction(time.Now()) }

func (d *Daemon) named(id string) incident.Named {
	n := incident.Named{ID: id}
	if s := d.prof.Server(id); s != nil {
		n.Name = s.Name
	}
	return n
}

// subStatus is what a subscription's panel last said about its term.
func (d *Daemon) subStatus() []incident.SubStatus {
	list := make([]incident.SubStatus, 0, len(d.prof.Subs))
	for _, s := range d.prof.Subs {
		list = append(list, incident.SubStatus{ID: s.ID, Name: s.Name, ExpireAt: s.ExpireAt, Used: s.UsedBytes, Total: s.TotalBytes})
	}
	return list
}

// serverExpired reports whether the server comes from a subscription that
// has run out: the likeliest reason for it to be silent.
func (d *Daemon) serverExpired(id string, now time.Time) bool {
	srv := d.prof.Server(id)
	if srv == nil || srv.Sub == "" {
		return false
	}
	for _, s := range d.subStatus() {
		if s.ID == srv.Sub {
			return incident.Expired(now, s)
		}
	}
	return false
}

func serverVerdict(v string) bool {
	return v == proto.VerdictServerDown || v == proto.VerdictAllServersDown
}

// judge writes the diagnosis into State and lets the incidents react to it.
// It runs after every event and on every tick, so it must change nothing
// when nothing changed.
func (d *Daemon) judge(now time.Time) {
	diag := d.hm.Diagnosis()
	changed := false

	hint := diag.Hint
	if serverVerdict(diag.Verdict) && d.serverExpired(diag.Server, now) {
		hint = health.HintExpired
	}
	h := proto.Health{Verdict: diag.Verdict, Internet: diag.Internet, Network: diag.Network}
	if !diag.Since.IsZero() {
		since := diag.Since.UTC().Truncate(time.Second)
		h.Since = &since
	}
	if text := incident.HintText(hint); text != "" && serverVerdict(diag.Verdict) {
		h.Hint = &text
	}
	if !sameHealth(d.st.Health, h) {
		was := d.st.Health.Verdict
		d.st.Health = h
		changed = true
		if was != h.Verdict {
			d.opts.Logf("health: %s -> %s", was, h.Verdict)
			d.verdictChanged(now, diag)
		}
	}

	// The selected server's own probes are its ping. Only a newer answer
	// counts: once the server is marked silent, the machine's memory of its
	// last good probe must not bring it back to life.
	if diag.Server != "" && diag.PingAt.After(d.pings[diag.Server].at) {
		if p := (ping{ms: diag.Ping, at: diag.PingAt, alive: true}); d.pings[diag.Server] != p {
			d.pings[diag.Server] = p
			d.statsDirty = d.stats.Answered(diag.Server, diag.Ping, diag.PingAt) || d.statsDirty
			d.refresh()
			changed = true
		}
	}

	in := incident.Input{
		Diag: diag, Level: d.st.Settings.Notify, Server: d.named(diag.Server), Hint: hint,
		Off: d.st.VPN.Phase == proto.PhaseOff || d.st.VPN.Phase == proto.PhaseStopping,
	}
	if d.st.VPN.FailText != nil {
		in.FailText = *d.st.VPN.FailText
	}
	in.Blocking = d.st.Guard.Blocking
	moveTo := ""
	if diag.Verdict == proto.VerdictServerDown && len(diag.Alive) > 0 {
		offer := d.named(diag.Alive[0])
		in.Switch = &offer
		if d.st.Settings.Failover {
			if moveTo = d.firstFavorite(diag.Alive); moveTo != "" {
				moved := d.named(moveTo)
				in.Moved = &moved
			}
		}
	}
	d.emit(d.im.Observe(now, in))

	if diag.Verdict == proto.VerdictOK && !d.toldOn {
		// "Подключён" is said once per start, when the server has answered.
		d.toldOn = true
		d.toast(d.im.Notice(d.st.Settings.Notify, incident.Connected(d.named(diag.Server), diag.Ping))...)
	}
	if changed {
		d.touch()
	}
	if moveTo != "" {
		// The fallback server: the incident above already says so. The
		// daemon never goes back by itself; the next move is the user's.
		d.opts.Logf("failover: %s is down, switching to %s", diag.Server, moveTo)
		if err := d.selectServer(moveTo, false); err != nil {
			d.opts.Logf("failover to %s: %v", moveTo, err)
		}
	}
}

func sameHealth(a, b proto.Health) bool {
	eqTime := func(x, y *time.Time) bool { return (x == nil) == (y == nil) && (x == nil || x.Equal(*y)) }
	eqText := func(x, y *string) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return a.Verdict == b.Verdict && a.Internet == b.Internet && a.Network == b.Network &&
		eqTime(a.Since, b.Since) && eqText(a.Hint, b.Hint)
}

// verdictChanged keeps the server list in step with a new verdict.
func (d *Daemon) verdictChanged(now time.Time, diag health.Diagnosis) {
	if !serverVerdict(diag.Verdict) || diag.Server == "" {
		return
	}
	d.pings[diag.Server] = ping{at: now}
	if diag.Verdict == proto.VerdictServerDown {
		// Others answered, so the miss is this server's own.
		d.statsDirty = d.stats.Missed(diag.Server, now) || d.statsDirty
	}
	d.refresh()
}

// firstFavorite picks the fallback: the first of the user's favourites, in
// their order, that has just answered.
func (d *Daemon) firstFavorite(alive []string) string {
	for _, fav := range d.prof.Favorites {
		for _, id := range alive {
			if id == fav {
				return fav
			}
		}
	}
	return ""
}

// emit carries out what the incident manager decided.
func (d *Daemon) emit(out incident.Output) {
	for _, e := range out.Journal {
		d.logFor(e.Incident, e.Cat, e.Level, e.Text)
	}
	d.toast(out.Toasts...)
	if out.Changed {
		d.st.Incidents = d.im.Incidents()
		d.touch()
	}
}

// toast sends notifications to whoever shows them. The daemon starts with
// the machine and the application with the user's session, so there may be
// nobody yet: a toast about a problem then waits for the first listener
// instead of being said to an empty room.
func (d *Daemon) toast(list ...proto.Toast) {
	for _, t := range list {
		if t.Actions == nil {
			t.Actions = []proto.ToastAction{}
		}
		if d.srv.Subscribers(proto.TopicToast) > 0 {
			delete(d.held, t.ID)
			d.srv.Publish(proto.TopicToast, t)
			continue
		}
		_, waiting := d.held[t.ID]
		switch {
		case strings.HasPrefix(t.ID, "e-"):
			// Plain news ("подключён") is stale by the time anyone comes.
		case t.Close, waiting && t.Severity == proto.SeverityInfo:
			// The problem came and went unseen; the journal has it.
			delete(d.held, t.ID)
		default:
			d.held[t.ID] = t
		}
	}
}

// deliverHeld gives a client that has just subscribed to toasts the ones
// that were waiting for a listener.
func (d *Daemon) deliverHeld(c *ipc.Conn) {
	ids := make([]string, 0, len(d.held))
	for id := range d.held {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c.Notify(proto.TopicToast, d.held[id])
	}
	d.held = map[string]proto.Toast{}
}

// checkSubs looks at the subscriptions' terms and traffic.
func (d *Daemon) checkSubs() {
	now := time.Now()
	d.subsChecked = now
	before := d.im.Warned()
	d.emit(d.im.Subs(now, d.st.Settings.Notify, d.subStatus()))
	after := d.im.Warned()
	same := len(before) == len(after)
	for k, v := range after {
		same = same && before[k] == v
	}
	if !same {
		if err := store.SaveJSON(d.opts.Paths.WarnedFile(), after); err != nil {
			d.opts.Logf("saving the subscription warnings: %v", err)
		}
	}
}

// ---- the clock -------------------------------------------------------------

// tick moves the automata's clock. It also is how sleep is noticed: the
// ticker does not fire while the machine sleeps, so a long gap between two
// ticks means it did. The wall clock is compared on purpose - Round(0)
// strips Go's monotonic reading, which on Windows stands still during
// sleep. A clock set forward by hand reads as a wake-up too; all that costs
// is a quiet quarter of a minute.
func (d *Daemon) tick() {
	wall := time.Now().Round(0)
	slept := !d.lastTick.IsZero() && wall.Sub(d.lastTick) > d.tune.SleepGap
	d.lastTick = wall
	// Before the automata: "blocking" is a matter of time, and what the
	// incidents say this tick depends on it.
	d.syncGuard(wall, false)
	if slept {
		d.opts.Logf("health: woke up after a gap in the clock")
		d.feed(health.Wake{})
	} else {
		d.feed(health.Tick{})
	}
	if wall.Sub(d.subsChecked) >= time.Hour {
		d.checkSubs()
	}
	if d.statsDirty && wall.Sub(d.statsSaved) >= 10*time.Minute {
		d.saveStats(wall)
	}
}

// Woke tells the daemon the machine has just come out of sleep. The service
// manager says so before the first tick after the lid opens; the clock gap
// in tick() would notice it a second or so later anyway, so this is the same
// signal arriving earlier, not a second mechanism. Taking the tick's clock
// along keeps the gap from reporting the same wake-up twice.
func (d *Daemon) Woke() {
	d.post(func() {
		d.lastTick = time.Now().Round(0)
		d.opts.Logf("health: the system says it has woken up")
		d.feed(health.Wake{})
	})
}

func (d *Daemon) saveStats(now time.Time) {
	d.statsDirty, d.statsSaved = false, now
	if err := d.stats.Save(d.opts.Paths.StatsFile(), now); err != nil {
		d.opts.Logf("saving the stats: %v", err)
	}
}

// watchNetwork starts the route monitor; its observations come back into
// the loop.
func (d *Daemon) watchNetwork() (stop func()) {
	report := func(s netmon.State) {
		d.post(func() {
			d.opts.Logf("network: default route present: %v (IPv4 %v, IPv6 %v)", s.Up(), s.V4, s.V6)
			d.feed(health.Network{Up: s.Up()})
			d.noteIPv6(s)
		})
	}
	if d.opts.WatchNetwork != nil {
		return d.opts.WatchNetwork(report)
	}
	m := netmon.Start(d.tune.NetDebounce, []string{confgen.TUNName}, report)
	return m.Close
}

// noteIPv6 follows whether the network has a way out over IPv6 at all. The
// TUN carries IPv6 only where there is some (see ipv6Mode), so when that
// changes a running engine is restarted onto a new config. A network that is
// gone altogether says nothing about IPv6 and is not taken for a change.
func (d *Daemon) noteIPv6(s netmon.State) {
	if !s.Up() {
		return
	}
	none := !s.V6
	if d.noV6.Swap(none) == none {
		return
	}
	d.opts.Logf("network: IPv6 way out: %v", s.V6)
	if d.opts.NoTUN || d.st.Settings.IPv6 != proto.IPv6Tunnel {
		return // the config does not depend on it
	}
	why := "в сети появился IPv6"
	if none {
		why = "в сети больше нет IPv6"
	}
	d.applyPlanChange(why)
}

// ---- probes ----------------------------------------------------------------

var errNoEngine = errors.New("the engine is not running")

func delay(api *clash.Client, tag, url string, timeout time.Duration) (int, error) {
	if api == nil {
		return 0, errNoEngine
	}
	return api.DelayURL(context.Background(), tag, url, timeout)
}

// probe runs one of the machine's requests through the engine, off the
// loop, and posts the answer back.
func (d *Daemon) probe(c health.Command) {
	api := d.api.Load()
	switch c := c.(type) {
	case health.ProbeServer:
		go func() {
			ms, err := delay(api, c.Server, c.URL, c.Timeout)
			d.post(func() { d.feed(health.ServerResult{Gen: c.Gen, OK: err == nil, Ms: ms}) })
		}()
	case health.ProbeDirect:
		go func() {
			ok := false
			for _, u := range health.DirectURLs {
				// The second address is asked only if the first is silent.
				if _, err := delay(api, confgen.DirectTag, u, c.Timeout); err == nil {
					ok = true
					break
				}
			}
			d.post(func() { d.feed(health.DirectResult{Gen: c.Gen, OK: ok}) })
		}()
	case health.ProbeOthers:
		pool := d.others(c.Except, c.Max)
		go func() {
			ms := make([]int, len(pool))
			ok := make([]bool, len(pool))
			var wg sync.WaitGroup
			for i, id := range pool {
				wg.Add(1)
				go func(i int, id string) {
					defer wg.Done()
					var err error
					ms[i], err = delay(api, id, c.URL, c.Timeout)
					ok[i] = err == nil
				}(i, id)
			}
			wg.Wait()
			d.post(func() {
				now := time.Now()
				var alive []string
				for i, id := range pool {
					if ok[i] {
						alive = append(alive, id)
						d.pings[id] = ping{ms: ms[i], at: now, alive: true}
					} else {
						d.pings[id] = ping{at: now}
					}
				}
				d.refresh()
				d.touch()
				d.feed(health.OthersResult{Gen: c.Gen, Alive: alive, Tested: len(pool)})
			})
		}()
	}
}

// others picks the servers to try when the selected one is silent: the
// favourites in the user's order - the fallback is chosen among them - and
// then the best of the rest.
func (d *Daemon) others(except string, n int) []string {
	var pool []string
	taken := map[string]bool{except: true}
	add := func(id string) {
		if !taken[id] && d.prof.Server(id) != nil && len(pool) < n {
			taken[id] = true
			pool = append(pool, id)
		}
	}
	for _, id := range d.prof.Favorites {
		add(id)
	}
	rest := make([]string, 0, len(d.prof.Servers))
	for _, s := range d.prof.Servers {
		rest = append(rest, s.ID)
	}
	for _, id := range d.stats.Order(rest) {
		add(id)
	}
	return pool
}

// ---- the engines' logs -----------------------------------------------------

// hintGate lets one line per server and cause through every couple of
// seconds. A dead server fails every connection a browser opens, dozens a
// second, and each of those lines says the same.
type hintGate struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (g *hintGate) pass(key string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
		g.last = map[string]time.Time{}
	}
	if now.Sub(g.last[key]) < 2*time.Second {
		return false
	}
	g.last[key] = now
	return true
}

// onLine looks at a line of an engine's log for the reason a server does
// not answer. It runs in the supervisor's goroutine.
func (d *Daemon) onLine(source, line string) {
	server, hint, ok := health.ParseLine(source, line)
	if !ok || !d.hints.pass(server+"|"+string(hint), time.Now()) {
		return
	}
	d.post(func() {
		if server == "" {
			// Xray does not say which server a line is about. It can only
			// be the one in use, and only if that one goes through Xray.
			sel := d.prof.Server(d.prof.Selected)
			if sel == nil || sel.Via != proto.ViaXray {
				return
			}
			server = sel.ID
		}
		d.feed(health.LogHint{Server: server, Hint: hint})
	})
}

// ---- journal.list and incident.action --------------------------------------

func (d *Daemon) journalList(c rpcCall) (any, *proto.Error) {
	var p proto.JournalListParams
	if len(c.params) > 0 {
		if err := params(c, &p); err != nil {
			return nil, err
		}
	}
	return d.journal.List(p), nil
}

func (d *Daemon) incidentAction(c rpcCall) (any, *proto.Error) {
	var p proto.IncidentActionParams
	if err := params(c, &p); err != nil {
		return nil, err
	}
	switch {
	case strings.HasPrefix(p.Action, proto.ActionSwitch):
		return nil, d.selectServer(strings.TrimPrefix(p.Action, proto.ActionSwitch), true)
	case p.Action == proto.ActionRetry:
		switch d.st.VPN.Phase {
		case proto.PhaseOff, proto.PhaseFailed:
			return d.vpnStart()
		}
		return d.vpnRestart()
	case p.Action == proto.ActionUpdateSubs:
		if err := d.jobBusy(); err != nil {
			return nil, err
		}
		if len(d.prof.Subs) == 0 {
			return nil, proto.Errorf(proto.CodeNotFound, "Нет ни одной подписки. Сначала добавь её.")
		}
		d.acted()
		d.startSubsJob(&subsJob{all: true})
		return nil, nil
	case p.Action == proto.ActionRelease:
		// "Let the internet through without the VPN" is switching the VPN
		// off: the one ordinary way the guard is ever lifted.
		return d.vpnStop()
	case p.Action == proto.ActionOpen, p.Action == proto.ActionDismiss:
		return nil, nil // the application's own; nothing for the daemon to do
	}
	return nil, proto.Errorf(proto.CodeInvalidParams, "Служба не знает действие «%s».", p.Action)
}
