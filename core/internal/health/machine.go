// Package health turns what the daemon can see - probes, traffic, the
// network, the engines - into one verdict that only moves when the evidence
// holds.
//
// Machine is a pure automaton: it is fed events together with the time they
// happened and answers with the probes it wants run. It starts no goroutines
// and never looks at the clock, so a whole day of trouble can be replayed in
// a test in microseconds.
package health

import (
	"time"

	"strelka/proto"
)

// Hint is a guess at why a server does not answer, read off the engines'
// logs. The wording for the user lives with the incidents.
type Hint string

const (
	HintNone        Hint = ""
	HintUnreachable Hint = "unreachable" // timeouts, refused connections
	HintKeys        Hint = "keys"        // the server turned the handshake down
	HintAuth        Hint = "auth"        // the server does not know this user
	HintReset       Hint = "reset"       // cut right after the TLS hello
	HintBlocked     Hint = "blocked"     // the same cut on every server
	HintExpired     Hint = "expired"     // set by the daemon: the subscription ran out
)

// Event is something that happened. Every one of them goes through Step.
type Event interface{ event() }

type (
	// Phase is a change of vpn.phase; Reason comes with "failed".
	Phase struct{ Phase, Reason string }
	// Helper tells whether the Xray helper is down while the VPN is on.
	Helper struct{ Down bool }
	// Network is the monitor's observation after a change: is there a
	// default route at all.
	Network struct{ Up bool }
	// Wake means the machine has slept.
	Wake struct{}
	// Select is the selector being pointed at a server.
	Select struct{ Server string }
	// ServerResult answers ProbeServer.
	ServerResult struct {
		Gen uint64
		OK  bool
		Ms  int
	}
	// DirectResult answers ProbeDirect.
	DirectResult struct {
		Gen uint64
		OK  bool
	}
	// OthersResult answers ProbeOthers: which of the tested servers are alive.
	OthersResult struct {
		Gen    uint64
		Alive  []string
		Tested int
	}
	// Traffic means bytes came back through the VPN since the last look.
	Traffic struct{}
	// LogHint is an error line of an engine's log, already classified.
	LogHint struct {
		Server string
		Hint   Hint
	}
	// Tick only moves the clock.
	Tick struct{}
)

func (Phase) event()        {}
func (Helper) event()       {}
func (Network) event()      {}
func (Wake) event()         {}
func (Select) event()       {}
func (ServerResult) event() {}
func (DirectResult) event() {}
func (OthersResult) event() {}
func (Traffic) event()      {}
func (LogHint) event()      {}
func (Tick) event()         {}

// Command is a probe the machine wants run. The answer comes back as the
// matching result event carrying the same Gen: the machine drops answers to
// questions it has since lost interest in.
type Command interface{ command() }

type (
	// ProbeServer fetches URL through the selected server.
	ProbeServer struct {
		Gen     uint64
		Server  string
		URL     string
		Timeout time.Duration
	}
	// ProbeDirect fetches DirectURLs past the VPN, the second if the first
	// did not answer.
	ProbeDirect struct {
		Gen     uint64
		Timeout time.Duration
	}
	// ProbeOthers tests up to Max servers other than Except.
	ProbeOthers struct {
		Gen     uint64
		Except  string
		Max     int
		URL     string
		Timeout time.Duration
	}
)

func (ProbeServer) command() {}
func (ProbeDirect) command() {}
func (ProbeOthers) command() {}

// Diagnosis is what the machine currently makes of it all.
type Diagnosis struct {
	Verdict string // one of proto.Verdict*
	Since   time.Time
	Reason  string // with engine_failed
	Server  string // the server the verdict is about
	Hint    Hint
	// Internet and Network are the two facts the window shows next to the
	// verdict; both are assumed true until something says otherwise.
	Internet bool
	Network  bool
	// Alive lists the other servers that answered when this one did not.
	Alive []string
	// Ping is the selected server's last answer; zero PingAt means none yet.
	Ping   int
	PingAt time.Time
}

type fact struct {
	at      time.Time // zero: not known
	ok      bool
	pending bool // the question is out
}

type seen struct {
	hint Hint
	at   time.Time
}

// Machine is the diagnosis automaton. The zero value is not usable; see New.
type Machine struct {
	t Tuning

	phase      string
	reason     string
	helperDown bool
	server     string

	netKnown bool
	netUp    bool
	// netDoubt: the monitor said "no network", yet something has answered
	// since. The answer wins until the monitor speaks again.
	netDoubt bool

	// gen grows whenever the subject changes (another server, another
	// network, a restart). Results of probes sent before that are dropped.
	gen         uint64
	settleUntil time.Time
	// fresh: nothing is known yet about the server in its present
	// circumstances, so the verdict must not vouch for it either way.
	fresh bool

	probeDue time.Time // zero: none scheduled
	probing  bool
	failed   int // probes failed in a row; picks the address of the next one

	misses    int // unconfirmed misses in a row
	firstMiss time.Time
	retry     int
	retryDue  time.Time // when the last re-check was meant to leave
	down      bool // the server is confirmed silent
	oks       int  // answers in a row since it went down
	lastRx    time.Time

	direct fact
	others fact
	alive  []string

	window   []bool // the last probes, true is an answer; outages are not kept
	degraded bool
	usual    float64 // the server's ordinary delay, ms
	usualN   int
	slowAt   time.Time // when delays first went far above usual
	slowN    int

	hints map[string]seen

	ping   int
	pingAt time.Time

	verdict string
	since   time.Time
	hint    Hint
}

// New returns a machine for a VPN that is off.
func New(t Tuning) *Machine {
	return &Machine{t: t, phase: proto.PhaseOff, verdict: proto.VerdictSettling, hints: map[string]seen{}}
}

func (m *Machine) on() bool { return m.phase == proto.PhaseOn }

// Step applies one event and returns the probes to run now.
func (m *Machine) Step(now time.Time, ev Event) []Command {
	switch e := ev.(type) {
	case Phase:
		was := m.phase
		m.phase, m.reason = e.Phase, e.Reason
		switch {
		case !m.on():
			m.clear()
		case was != proto.PhaseOn:
			m.restart(now)
		}
	case Helper:
		back := m.helperDown && !e.Down
		m.helperDown = e.Down
		if e.Down {
			m.clear()
		} else if back && m.on() {
			m.restart(now)
		}
	case Network:
		first := !m.netKnown
		m.netKnown, m.netUp, m.netDoubt = true, e.Up, false
		// The first observation is the monitor introducing itself, not a
		// change of network.
		if !first && m.on() {
			m.settle(now)
		}
	case Wake:
		if m.on() {
			m.settle(now)
		}
	case Select:
		if e.Server != m.server {
			m.server = e.Server
			if m.on() {
				m.restart(now)
			}
		}
	case ServerResult:
		if e.Gen == m.gen && m.probing {
			m.probing = false
			if e.OK {
				m.hit(now, e.Ms)
			} else {
				m.miss(now)
			}
		}
	case DirectResult:
		if e.Gen == m.gen {
			m.direct = fact{at: now, ok: e.OK}
			if e.OK {
				m.netDoubt = true
			}
		}
	case OthersResult:
		if e.Gen == m.gen {
			m.others = fact{at: now, ok: len(e.Alive) > 0}
			m.alive = append([]string(nil), e.Alive...)
			if len(e.Alive) > 0 {
				m.netDoubt = true
			}
		}
	case Traffic:
		if m.on() && !m.helperDown && !now.Before(m.settleUntil) {
			m.proven(now)
		}
	case LogHint:
		m.hints[e.Server] = seen{hint: e.Hint, at: now}
	case Tick:
	}
	cmds := m.plan(now)
	m.judge(now)
	return cmds
}

// clear forgets everything that was learnt by probing.
func (m *Machine) clear() {
	m.gen++
	m.settleUntil = time.Time{}
	m.fresh = false
	m.probeDue, m.probing, m.failed = time.Time{}, false, 0
	m.misses, m.retry, m.down, m.oks = 0, 0, false, 0
	m.lastRx = time.Time{}
	m.direct, m.others, m.alive = fact{}, fact{}, nil
	m.window, m.degraded = nil, false
	m.usual, m.usualN, m.slowAt, m.slowN = 0, 0, time.Time{}, 0
	m.ping, m.pingAt = 0, time.Time{}
}

// restart begins anew with a server nothing is known about: after a start,
// a switch, the helper coming back. It is probed at once.
func (m *Machine) restart(now time.Time) {
	m.clear()
	m.fresh = true
	m.probeDue = now
}

// settle is restart with a pause first: the network has changed or the
// machine woke up, and probing into that would only count failures that
// mean nothing.
func (m *Machine) settle(now time.Time) {
	m.clear()
	m.fresh = true
	m.settleUntil = now.Add(m.t.SettleAfterNet)
	m.probeDue = m.settleUntil
}

func (m *Machine) hit(now time.Time, ms int) {
	m.ping, m.pingAt = ms, now
	m.failed = 0
	m.netDoubt = true
	if m.down {
		if m.oks++; m.oks < m.t.RecoverOKs {
			m.probeDue = now.Add(m.t.RecoverRetry)
			return
		}
		m.recover()
	} else {
		m.push(true)
		m.pace(ms, now)
	}
	m.misses, m.fresh = 0, false
	m.probeDue = now.Add(m.t.ProbeEvery)
}

func (m *Machine) miss(now time.Time) {
	m.failed++
	if m.down {
		m.oks = 0
		m.probeDue = now.Add(m.t.DownProbeEvery)
		return
	}
	m.push(false)
	if m.misses == 0 {
		m.firstMiss, m.retry, m.retryDue = now, 0, now
	}
	m.misses++
	if m.misses >= m.t.ConfirmMisses && now.Sub(m.firstMiss) >= m.t.ConfirmSpan {
		m.down, m.oks = true, 0
		m.probeDue = now.Add(m.t.DownProbeEvery)
		return
	}
	// The re-checks are timed from the first miss, not from one another: a
	// probe that took its whole timeout must not push the verdict out.
	next := m.retryDue.Add(m.t.RetryEvery)
	if m.retry < len(m.t.RetryAt) {
		next = m.firstMiss.Add(m.t.RetryAt[m.retry])
		m.retry++
	}
	m.retryDue = next
	if next.Before(now) {
		next = now
	}
	m.probeDue = next
}

// proven is called when real traffic came back through the VPN: the server
// is alive whatever the probes say.
func (m *Machine) proven(now time.Time) {
	m.lastRx = now
	m.netDoubt = true
	if m.down {
		m.recover()
	}
	if m.misses > 0 || m.fresh {
		m.misses, m.fresh, m.failed = 0, false, 0
		if !m.probing {
			m.probeDue = now.Add(m.t.ProbeEvery)
		}
	}
}

func (m *Machine) recover() {
	m.down, m.oks, m.misses = false, 0, 0
	m.direct, m.others, m.alive = fact{}, fact{}, nil
	// The outage is an incident of its own; it must not linger on as
	// "unstable" for the next ten minutes.
	m.window, m.degraded = nil, false
	m.slowAt, m.slowN = time.Time{}, 0
}

func (m *Machine) push(ok bool) {
	m.window = append(m.window, ok)
	if n := len(m.window) - m.t.Window; n > 0 {
		m.window = append(m.window[:0], m.window[n:]...)
	}
}

// pace keeps the server's usual delay and notices when answers come far
// slower than that.
func (m *Machine) pace(ms int, now time.Time) {
	v := float64(ms)
	if m.usualN >= 5 && v > m.usual*m.t.SlowFactor && time.Duration(ms)*time.Millisecond > m.t.SlowFloor {
		if m.slowAt.IsZero() {
			m.slowAt, m.slowN = now, 0
		}
		m.slowN++
		return // a slow answer is not what "usual" is made of
	}
	m.slowAt, m.slowN = time.Time{}, 0
	m.usualN++
	// A plain mean while there are few samples, then a slow average that
	// follows the server through the day.
	alpha := 1 / float64(m.usualN)
	if alpha < 0.1 {
		alpha = 0.1
	}
	m.usual += alpha * (v - m.usual)
}

func (m *Machine) stale(f fact, now time.Time, keep time.Duration) bool {
	return !f.pending && (f.at.IsZero() || now.Sub(f.at) >= keep)
}

// plan decides which probes are due.
func (m *Machine) plan(now time.Time) []Command {
	if !m.on() || m.helperDown || now.Before(m.settleUntil) {
		return nil
	}
	var out []Command
	if !m.probing && !m.probeDue.IsZero() && !now.Before(m.probeDue) {
		calm := !m.fresh && !m.down && m.misses == 0 && m.slowAt.IsZero()
		if calm && !m.lastRx.IsZero() && now.Sub(m.lastRx) < m.t.TrafficFresh {
			// The server has just proved itself with real traffic; look
			// again when that proof goes stale.
			m.probeDue = m.lastRx.Add(m.t.TrafficFresh)
		} else {
			out = append(out, ProbeServer{
				Gen: m.gen, Server: m.server, Timeout: m.t.ProbeTimeout,
				URL: ServerURLs[m.failed%len(ServerURLs)],
			})
			m.probing, m.probeDue = true, time.Time{}
		}
	}
	// The second miss is when it starts to look serious: asking about the
	// internet now has the answer ready by the time the third one confirms.
	if (m.down || m.misses >= 2) && m.stale(m.direct, now, m.t.DirectFresh) {
		out = append(out, ProbeDirect{Gen: m.gen, Timeout: m.t.ProbeTimeout})
		m.direct.pending = true
	}
	if m.down && !m.direct.at.IsZero() && m.direct.ok && m.stale(m.others, now, m.t.OthersFresh) {
		out = append(out, ProbeOthers{
			Gen: m.gen, Except: m.server, Max: m.t.OthersMax, Timeout: m.t.ProbeTimeout,
			URL: ServerURLs[0],
		})
		m.others.pending = true
	}
	return out
}

func (m *Machine) networkGone() bool { return m.netKnown && !m.netUp && !m.netDoubt }

func (m *Machine) judge(now time.Time) {
	v := m.verdictNow(now)
	changed := v != m.verdict
	if changed {
		m.verdict, m.since = v, now
	}
	h := m.hintNow(now)
	// A hint stays with its verdict: the log lines behind it stop coming as
	// soon as programs give up trying, and the text must not vanish then.
	if h != HintNone || changed {
		m.hint = h
	}
}

func (m *Machine) verdictNow(now time.Time) string {
	switch m.phase {
	case proto.PhaseFailed:
		return proto.VerdictEngineFailed
	case proto.PhaseRecovering:
		return proto.VerdictEngineRecovering
	case proto.PhaseOn:
	default:
		return proto.VerdictSettling
	}
	switch {
	case m.helperDown:
		return proto.VerdictHelperDown
	case now.Before(m.settleUntil):
		return proto.VerdictSettling
	case m.networkGone():
		return proto.VerdictNoNetwork
	case m.down:
		if v, ok := m.classify(); ok {
			return v
		}
		// Still finding out whose fault it is: keep saying what was said.
		switch m.verdict {
		case proto.VerdictServerDown, proto.VerdictAllServersDown, proto.VerdictNoInternet,
			proto.VerdictOK, proto.VerdictDegraded:
			return m.verdict
		}
		return proto.VerdictSettling
	case m.fresh:
		return proto.VerdictSettling
	case m.unstable(now):
		return proto.VerdictDegraded
	}
	return proto.VerdictOK
}

// classify names the culprit of a confirmed outage; false while the facts
// it needs are still out.
func (m *Machine) classify() (string, bool) {
	if m.direct.at.IsZero() {
		return "", false
	}
	if !m.direct.ok {
		return proto.VerdictNoInternet, true
	}
	if m.others.at.IsZero() {
		return "", false
	}
	if m.others.ok {
		return proto.VerdictServerDown, true
	}
	return proto.VerdictAllServersDown, true
}

func (m *Machine) unstable(now time.Time) bool {
	lost := 0
	for _, ok := range m.window {
		if !ok {
			lost++
		}
	}
	slow := !m.slowAt.IsZero() && m.slowN >= 3 && now.Sub(m.slowAt) >= m.t.SlowFor
	if m.degraded {
		m.degraded = slow || lost > m.t.DegradedClear
	} else {
		m.degraded = slow || lost >= m.t.DegradedMisses
	}
	return m.degraded
}

func (m *Machine) hintNow(now time.Time) Hint {
	if m.verdict != proto.VerdictServerDown && m.verdict != proto.VerdictAllServersDown {
		return HintNone
	}
	recent := func(s seen) bool { return now.Sub(s.at) <= m.t.HintFresh }
	if m.verdict == proto.VerdictAllServersDown {
		// One server cutting the handshake is that server's business. Several
		// doing the same at once is somebody in between.
		cut := 0
		for _, s := range m.hints {
			if s.hint == HintReset && recent(s) {
				cut++
			}
		}
		if cut >= 2 {
			return HintBlocked
		}
	}
	if s, ok := m.hints[m.server]; ok && recent(s) && s.hint != HintReset {
		return s.hint
	}
	return HintNone
}

// Diagnosis reports the present verdict.
func (m *Machine) Diagnosis() Diagnosis {
	d := Diagnosis{
		Verdict: m.verdict, Since: m.since, Server: m.server, Hint: m.hint,
		Network: !m.networkGone(), Ping: m.ping, PingAt: m.pingAt,
	}
	d.Internet = d.Network && (m.direct.at.IsZero() || m.direct.ok)
	if m.verdict == proto.VerdictEngineFailed {
		d.Reason = m.reason
	}
	if m.verdict == proto.VerdictServerDown {
		d.Alive = append([]string(nil), m.alive...)
	}
	return d
}
