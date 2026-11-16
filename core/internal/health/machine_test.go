package health

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"strelka/proto"
)

// world plays the daemon and everything beyond it for a Machine: it ticks a
// fake clock once a second, runs the probes the machine asks for against a
// network described by a few switches, and hands the answers back when they
// would have arrived.
type world struct {
	t   *testing.T
	m   *Machine
	now time.Time
	t0  time.Time

	serverUp  bool
	internet  bool
	others    []string      // the other servers that answer
	ping      int           // ms, a live server's answer
	missTakes time.Duration // how long a dead server takes to fail a probe

	nextTick time.Time
	pending  []arrival
	probes   []sent    // every server probe, in order
	asked    []string  // "direct" and "others", in order
	changes  []verdict // every change of the verdict
}

type arrival struct {
	at time.Time
	ev Event
}

type sent struct {
	at  time.Duration // since the start of the test
	url string
}

type verdict struct {
	at time.Duration
	v  string
}

func newWorld(t *testing.T) *world {
	t0 := time.Unix(1_800_000_000, 0)
	w := &world{
		t: t, m: New(Default()), now: t0, t0: t0, nextTick: t0.Add(time.Second),
		serverUp: true, internet: true, others: []string{"b-NL", "c-FI"}, ping: 80,
		missTakes: 5 * time.Second,
	}
	w.send(Network{Up: true})
	w.send(Select{Server: "a-DE"})
	return w
}

// started is a world whose VPN is on and has answered its first probe.
func started(t *testing.T) *world {
	w := newWorld(t)
	w.send(Phase{Phase: proto.PhaseOn})
	w.run(2 * time.Second)
	w.want(proto.VerdictOK)
	return w
}

func (w *world) send(ev Event) {
	w.t.Helper()
	for _, c := range w.m.Step(w.now, ev) {
		w.exec(c)
	}
	if v := w.m.Diagnosis().Verdict; len(w.changes) == 0 || w.changes[len(w.changes)-1].v != v {
		w.changes = append(w.changes, verdict{at: w.now.Sub(w.t0), v: v})
	}
}

func (w *world) exec(c Command) {
	later := func(d time.Duration, ev Event) {
		w.pending = append(w.pending, arrival{at: w.now.Add(d), ev: ev})
	}
	switch c := c.(type) {
	case ProbeServer:
		w.probes = append(w.probes, sent{at: w.now.Sub(w.t0), url: c.URL})
		if w.serverUp && w.internet {
			later(time.Duration(w.ping)*time.Millisecond, ServerResult{Gen: c.Gen, OK: true, Ms: w.ping})
		} else {
			later(w.missTakes, ServerResult{Gen: c.Gen})
		}
	case ProbeDirect:
		w.asked = append(w.asked, "direct")
		if w.internet {
			later(60*time.Millisecond, DirectResult{Gen: c.Gen, OK: true})
		} else {
			later(2*w.missTakes, DirectResult{Gen: c.Gen}) // both addresses were tried
		}
	case ProbeOthers:
		w.asked = append(w.asked, "others")
		alive := w.others
		if !w.internet {
			alive = nil
		}
		wait := 200 * time.Millisecond
		if len(alive) == 0 {
			wait = w.missTakes
		}
		later(wait, OthersResult{Gen: c.Gen, Alive: alive, Tested: 5})
	}
}

// run moves the clock forward by d, delivering ticks and probe answers in
// the order they fall due.
func (w *world) run(d time.Duration) {
	w.t.Helper()
	end := w.now.Add(d)
	for {
		first := -1
		for i, p := range w.pending {
			if !p.at.After(end) && !p.at.After(w.nextTick) && (first < 0 || p.at.Before(w.pending[first].at)) {
				first = i
			}
		}
		if first >= 0 {
			p := w.pending[first]
			w.pending = append(w.pending[:first], w.pending[first+1:]...)
			if p.at.After(w.now) {
				w.now = p.at
			}
			w.send(p.ev)
			continue
		}
		if w.nextTick.After(end) {
			w.now = end
			return
		}
		w.now = w.nextTick
		w.nextTick = w.nextTick.Add(time.Second)
		w.send(Tick{})
	}
}

// traffic runs the clock with bytes coming back through the VPN every two
// seconds, the way the daemon reports them.
func (w *world) traffic(d time.Duration) {
	w.t.Helper()
	for end := w.now.Add(d); w.now.Before(end); {
		w.run(2 * time.Second)
		w.send(Traffic{})
	}
}

func (w *world) verdict() string { return w.m.Diagnosis().Verdict }

func (w *world) want(v string) {
	w.t.Helper()
	if got := w.verdict(); got != v {
		w.t.Fatalf("через %v диагноз %q, жду %q (как менялся: %s)", w.now.Sub(w.t0), got, v, w.history())
	}
}

func (w *world) history() string {
	var parts []string
	for _, c := range w.changes {
		parts = append(parts, fmt.Sprintf("%v %s", c.at, c.v))
	}
	return strings.Join(parts, " → ")
}

// until runs the clock until the verdict is v and returns how long it took.
func (w *world) until(v string, limit time.Duration) time.Duration {
	w.t.Helper()
	from := w.now
	for w.verdict() != v {
		if w.now.Sub(from) >= limit {
			w.t.Fatalf("за %v диагноз так и не стал %q (как менялся: %s)", limit, v, w.history())
		}
		w.run(100 * time.Millisecond)
	}
	return w.now.Sub(from)
}

// saw reports whether the verdict has been v at any point since mark.
func (w *world) saw(v string, since time.Duration) bool {
	for _, c := range w.changes {
		if c.v == v && c.at >= since {
			return true
		}
	}
	return false
}

func (w *world) elapsed() time.Duration { return w.now.Sub(w.t0) }

// ---- the scenarios of the health rules ------------------------------------

// The complaint that started all this: one missed ping turned the shield
// yellow and the next good one turned it back, with a notification each way.
func TestOneMissAmongAnswersChangesNothing(t *testing.T) {
	w := started(t)
	w.run(5 * time.Minute)

	mark := w.elapsed()
	w.serverUp = false
	for n := len(w.probes); len(w.probes) == n; {
		w.run(time.Second)
	}
	w.run(w.missTakes) // that one probe is lost; the re-check gets through
	w.serverUp = true
	w.run(10 * time.Minute)

	for _, c := range w.changes {
		if c.at >= mark {
			t.Errorf("после одного промаха диагноз стал %q (%s)", c.v, w.history())
		}
	}
	w.want(proto.VerdictOK)
}

func TestThreeMissesWithInternetIsServerDown(t *testing.T) {
	for name, takes := range map[string]time.Duration{
		"сервер молчит до таймаута": 5 * time.Second,
		"сервер отказывает сразу":   50 * time.Millisecond,
	} {
		t.Run(name, func(t *testing.T) {
			w := started(t)
			w.missTakes = takes
			w.run(28 * time.Second) // the next calm probe is two seconds away
			w.serverUp = false
			w.run(2 * time.Second)
			took := w.until(proto.VerdictServerDown, time.Minute)

			// Counted from the first probe that did not get through.
			if took < 15*time.Second-takes || took > 20*time.Second {
				t.Errorf("диагноз через %v после начала проблемы, жду 15–20 с (%s)", took, w.history())
			}
			d := w.m.Diagnosis()
			if !d.Internet || !d.Network || d.Server != "a-DE" || len(d.Alive) != 2 || d.Alive[0] != "b-NL" {
				t.Errorf("диагноз: %+v", d)
			}
			for _, c := range w.changes {
				if c.v == proto.VerdictNoInternet || c.v == proto.VerdictAllServersDown || c.v == proto.VerdictSettling && c.at > 3*time.Second {
					t.Errorf("по пути был диагноз %q (%s)", c.v, w.history())
				}
			}
		})
	}
}

func TestSameMissesWithoutInternetIsNoInternet(t *testing.T) {
	w := started(t)
	w.run(28 * time.Second)
	w.internet = false
	w.until(proto.VerdictNoInternet, time.Minute)
	d := w.m.Diagnosis()
	if d.Internet || !d.Network {
		t.Errorf("internet=%v network=%v, жду false и true", d.Internet, d.Network)
	}
	if w.saw(proto.VerdictServerDown, 0) || w.saw(proto.VerdictAllServersDown, 0) {
		t.Errorf("сервер обвинили, хотя не было интернета: %s", w.history())
	}
	for _, a := range w.asked {
		if a == "others" {
			t.Error("другие серверы проверялись без интернета")
		}
	}

	// The internet comes back and so does the server: no detour through
	// "server down" on the way to "ok".
	w.internet = true
	w.until(proto.VerdictOK, time.Minute)
	if w.saw(proto.VerdictServerDown, 0) {
		t.Errorf("после возвращения интернета сервер объявили мёртвым: %s", w.history())
	}
}

func TestInternetBackButServerStillSilentBecomesServerDown(t *testing.T) {
	w := started(t)
	w.run(28 * time.Second)
	w.internet, w.serverUp = false, false
	w.until(proto.VerdictNoInternet, time.Minute)
	w.internet = true
	w.until(proto.VerdictServerDown, time.Minute)
}

func TestNoServerAnsweringIsAllServersDown(t *testing.T) {
	w := started(t)
	w.run(28 * time.Second)
	w.serverUp, w.others = false, nil
	w.until(proto.VerdictAllServersDown, time.Minute)
	if d := w.m.Diagnosis(); !d.Internet || len(d.Alive) != 0 {
		t.Errorf("диагноз: %+v", d)
	}

	// One of them wakes up: the picture is refreshed while the trouble lasts.
	w.others = []string{"c-FI"}
	w.until(proto.VerdictServerDown, 2*time.Minute)
}

// Waking from sleep: the network is not back yet and every probe is refused
// at once. Whatever was counted before the lid closed must not add up.
func TestSleepForAnHourIsSettlingNotAnOutage(t *testing.T) {
	w := started(t)
	w.run(28 * time.Second)
	w.serverUp = false
	w.run(9 * time.Second) // two misses on record when the lid closes

	w.pending = nil // nothing answers a sleeping machine
	w.now = w.now.Add(time.Hour)
	w.nextTick = w.now.Add(time.Second)
	mark := w.elapsed()
	w.send(Wake{})
	w.want(proto.VerdictSettling)

	w.run(10 * time.Second)
	w.want(proto.VerdictSettling)
	if n := len(w.probes); n > 0 && w.probes[n-1].at >= mark {
		t.Error("зонд ушёл в первые секунды после пробуждения")
	}
	w.serverUp = true // the network is back by the time the pause ends
	w.until(proto.VerdictOK, 10*time.Second)
	for _, c := range w.changes {
		if c.at >= mark && c.v != proto.VerdictSettling && c.v != proto.VerdictOK {
			t.Errorf("после сна был диагноз %q (%s)", c.v, w.history())
		}
	}
}

func TestTrafficOutweighsAMissedProbe(t *testing.T) {
	w := started(t)
	w.run(28 * time.Second)
	w.serverUp = false // the probe target is unreachable, the server is fine
	mark := w.elapsed()
	// Traffic that starts only after the first miss: the miss is forgotten.
	w.run(4 * time.Second)
	w.traffic(3 * time.Minute)
	for _, c := range w.changes {
		if c.at >= mark {
			t.Errorf("при живом трафике диагноз стал %q (%s)", c.v, w.history())
		}
	}
	w.want(proto.VerdictOK)
}

func TestCalmProbeIsSkippedWhileTrafficFlows(t *testing.T) {
	w := started(t)
	before := len(w.probes)
	w.traffic(5 * time.Minute)
	if n := len(w.probes) - before; n != 0 {
		t.Errorf("за пять минут с трафиком ушло %d зондов, жду ни одного", n)
	}
	w.run(2 * time.Minute)
	if n := len(w.probes) - before; n < 3 || n > 4 {
		t.Errorf("за две минуты тишины ушло %d зондов, жду по одному в 30 с", n)
	}
}

func TestRetriesComeQuicklyAndAlternateAddresses(t *testing.T) {
	w := started(t)
	w.missTakes = 10 * time.Millisecond
	w.run(28 * time.Second)
	w.serverUp = false
	from := len(w.probes)
	w.until(proto.VerdictServerDown, time.Minute)

	got := w.probes[from:]
	if len(got) < 5 {
		t.Fatalf("зондов до диагноза: %d, жду пять (промах, +2, +5, +10, +15)", len(got))
	}
	first := got[0].at
	for i, want := range []time.Duration{0, 2 * time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second} {
		// The machine is asked once a second, so a probe may leave late by that much.
		if off := got[i].at - first; off < want || off > want+1100*time.Millisecond {
			t.Errorf("зонд %d ушёл через %v после первого, жду %v", i+1, off, want)
		}
		if i > 0 && got[i].url == got[i-1].url {
			t.Errorf("зонды %d и %d ушли на один адрес %s", i, i+1, got[i].url)
		}
	}
	// Three quick refusals are not an outage: the evidence must span 12 s.
	if w.changes[len(w.changes)-1].at-first < 12*time.Second {
		t.Errorf("диагноз раньше, чем через 12 с после первого промаха (%s)", w.history())
	}
}

func TestRecoveryTakesTwoAnswersOrTraffic(t *testing.T) {
	w := started(t)
	w.run(28 * time.Second)
	w.serverUp = false
	w.until(proto.VerdictServerDown, time.Minute)

	w.serverUp = true
	took := w.until(proto.VerdictOK, time.Minute)
	if took > 13*time.Second {
		t.Errorf("сервер ожил, а диагноз ok только через %v", took)
	}
	answers := 0
	for i := len(w.probes) - 1; i >= 0 && w.probes[i].at > w.elapsed()-took; i-- {
		answers++
	}
	if answers != 2 {
		t.Errorf("до ok ушло %d зондов, жду два ответа подряд", answers)
	}

	// Down again; this time real traffic brings it back at once.
	w.run(20 * time.Second)
	w.serverUp = false
	w.until(proto.VerdictServerDown, 2*time.Minute)
	w.send(Traffic{})
	w.want(proto.VerdictOK)
}

func TestNetworkGoneIsNoNetworkAndComingBackSettles(t *testing.T) {
	w := started(t)
	w.run(10 * time.Second)
	w.serverUp, w.internet = false, false
	w.send(Network{Up: false})
	w.want(proto.VerdictSettling) // Wi-Fi may just be hopping to another point
	w.run(14 * time.Second)
	w.want(proto.VerdictSettling)
	w.run(2 * time.Second)
	w.want(proto.VerdictNoNetwork)
	if d := w.m.Diagnosis(); d.Network || d.Internet {
		t.Errorf("network=%v internet=%v, жду false", d.Network, d.Internet)
	}
	w.run(5 * time.Minute)
	w.want(proto.VerdictNoNetwork)

	w.serverUp, w.internet = true, true
	w.send(Network{Up: true})
	w.want(proto.VerdictSettling)
	took := w.until(proto.VerdictOK, 30*time.Second)
	if took < 15*time.Second {
		t.Errorf("ok через %v после смены сети, раньше 15 с диагнозов быть не должно", took)
	}
}

// The monitor can be wrong (an exotic adapter it does not count as a route).
// A server that answers is stronger evidence than a routing table.
func TestAnAnswerOverrulesTheMonitor(t *testing.T) {
	w := started(t)
	w.send(Network{Up: false})
	w.until(proto.VerdictOK, 30*time.Second)
	if d := w.m.Diagnosis(); !d.Network {
		t.Error("сервер отвечает, а network=false")
	}
}

// A hand-picked server must not inherit the green shield of the previous
// one: until its first probe nothing is known about it.
func TestSwitchingStartsOver(t *testing.T) {
	w := started(t)
	w.run(10 * time.Second)

	w.serverUp = false
	mark := w.elapsed()
	w.send(Select{Server: "b-NL"})
	w.want(proto.VerdictSettling)
	took := w.until(proto.VerdictServerDown, time.Minute)
	if took > 20*time.Second {
		t.Errorf("мёртвый сервер после переключения: диагноз через %v, жду до 20 с", took)
	}
	for _, c := range w.changes {
		if c.at > mark && c.v == proto.VerdictOK {
			t.Errorf("сервер, который ни разу не ответил, был %q (%s)", c.v, w.history())
		}
	}
	if d := w.m.Diagnosis(); d.Server != "b-NL" {
		t.Errorf("диагноз о сервере %q, жду b-NL", d.Server)
	}

	w.serverUp = true
	w.send(Select{Server: "c-FI"})
	w.want(proto.VerdictSettling)
	if took := w.until(proto.VerdictOK, 5*time.Second); took > time.Second {
		t.Errorf("живой сервер после переключения: ok через %v, жду с первым же ответом", took)
	}
}

func TestAnswersToOldQuestionsAreDropped(t *testing.T) {
	w := started(t)
	w.run(28 * time.Second)
	w.serverUp = false
	w.run(2500 * time.Millisecond) // a probe of a-DE is out and will fail
	if len(w.pending) == 0 {
		t.Fatal("в полёте нет ни одного зонда")
	}
	w.serverUp = true
	stale := w.pending[0]
	w.pending = nil
	w.send(Select{Server: "b-NL"}) // sends its own probe, answered below
	w.now = w.now.Add(4 * time.Second)
	w.send(stale.ev) // the old server's miss arrives late
	w.send(ServerResult{Gen: stale.ev.(ServerResult).Gen, OK: true, Ms: 1})
	w.want(proto.VerdictSettling)
	if d := w.m.Diagnosis(); !d.PingAt.IsZero() {
		t.Errorf("ответ прежнего сервера записан новому: пинг %d", d.Ping)
	}
}

func TestEnginePhasesMapToVerdicts(t *testing.T) {
	w := newWorld(t)
	w.want(proto.VerdictSettling) // off
	w.send(Phase{Phase: proto.PhaseStarting})
	w.want(proto.VerdictSettling)
	w.send(Phase{Phase: proto.PhaseOn})
	w.want(proto.VerdictSettling)
	w.run(time.Second)
	w.want(proto.VerdictOK)

	w.send(Phase{Phase: proto.PhaseRecovering})
	w.want(proto.VerdictEngineRecovering)
	w.run(30 * time.Second)
	if n := len(w.probes); n != 1 {
		t.Errorf("зондов %d: пока движок лежит, проверять нечем", n)
	}
	w.send(Phase{Phase: proto.PhaseOn})
	w.want(proto.VerdictSettling)
	w.run(time.Second)
	w.want(proto.VerdictOK)

	w.send(Phase{Phase: proto.PhaseFailed, Reason: proto.FailTUN})
	w.want(proto.VerdictEngineFailed)
	if d := w.m.Diagnosis(); d.Reason != proto.FailTUN {
		t.Errorf("причина %q, жду %q", d.Reason, proto.FailTUN)
	}
	w.send(Phase{Phase: proto.PhaseOff})
	w.want(proto.VerdictSettling)
}

func TestHelperDownHidesServerVerdicts(t *testing.T) {
	w := started(t)
	w.serverUp = false // the server goes through the helper
	w.send(Helper{Down: true})
	w.want(proto.VerdictHelperDown)
	w.run(2 * time.Minute)
	w.want(proto.VerdictHelperDown)
	if w.saw(proto.VerdictServerDown, 0) {
		t.Errorf("виноват помощник, а не сервер: %s", w.history())
	}
	w.serverUp = true
	w.send(Helper{Down: false})
	w.want(proto.VerdictSettling)
	w.until(proto.VerdictOK, 5*time.Second)
}

func TestEveryOtherProbeLostIsDegradedNotDown(t *testing.T) {
	w := started(t)
	w.missTakes = 100 * time.Millisecond
	for i := 0; i < 12 && w.verdict() != proto.VerdictDegraded; i++ {
		// One lost probe and its quick re-check that gets through.
		w.serverUp = false
		for n := len(w.probes); len(w.probes) == n; {
			w.run(time.Second)
		}
		w.run(500 * time.Millisecond)
		w.serverUp = true
		w.run(10 * time.Second)
	}
	w.want(proto.VerdictDegraded)
	if w.saw(proto.VerdictServerDown, 0) {
		t.Errorf("сервер отвечал через раз, а его объявили мёртвым: %s", w.history())
	}

	// It takes a clean run to be "ok" again, not the first good answer.
	w.run(time.Minute)
	w.want(proto.VerdictDegraded)
	w.until(proto.VerdictOK, 15*time.Minute)
}

func TestSlowAnswersForTwoMinutesAreDegraded(t *testing.T) {
	w := started(t)
	w.run(5 * time.Minute) // learns that 80 ms is usual

	w.ping = 200 // more than twice the usual, but under the floor
	w.run(5 * time.Minute)
	w.want(proto.VerdictOK)

	w.ping = 80
	w.run(3 * time.Minute)
	w.ping = 1500
	w.run(90 * time.Second)
	w.want(proto.VerdictOK) // slow, but not for long enough yet
	w.until(proto.VerdictDegraded, 2*time.Minute)

	w.ping = 80
	w.until(proto.VerdictOK, time.Minute)
}

func TestOutageDoesNotLeaveDegradedBehind(t *testing.T) {
	w := started(t)
	w.run(28 * time.Second)
	w.serverUp = false
	w.until(proto.VerdictServerDown, time.Minute)
	w.run(3 * time.Minute)
	w.serverUp = true
	w.until(proto.VerdictOK, time.Minute)
	w.run(10 * time.Minute)
	if w.saw(proto.VerdictDegraded, 0) {
		t.Errorf("после обычного падения и подъёма был диагноз degraded: %s", w.history())
	}
}

// Each verdict, and the least set of signals that leads to it.
func TestEveryVerdictHasItsSignals(t *testing.T) {
	cases := []struct {
		verdict string
		arrange func(w *world)
	}{
		{proto.VerdictOK, func(w *world) {}},
		{proto.VerdictSettling, func(w *world) { w.send(Select{Server: "b-NL"}) }},
		{proto.VerdictServerDown, func(w *world) { w.serverUp = false }},
		{proto.VerdictAllServersDown, func(w *world) { w.serverUp, w.others = false, nil }},
		{proto.VerdictNoInternet, func(w *world) { w.internet = false }},
		{proto.VerdictNoNetwork, func(w *world) { w.internet = false; w.send(Network{Up: false}) }},
		{proto.VerdictEngineRecovering, func(w *world) { w.send(Phase{Phase: proto.PhaseRecovering}) }},
		{proto.VerdictEngineFailed, func(w *world) { w.send(Phase{Phase: proto.PhaseFailed, Reason: proto.FailCrashLoop}) }},
		{proto.VerdictHelperDown, func(w *world) { w.send(Helper{Down: true}) }},
	}
	for _, c := range cases {
		t.Run(c.verdict, func(t *testing.T) {
			w := started(t)
			c.arrange(w)
			if w.verdict() != c.verdict {
				w.until(c.verdict, 2*time.Minute)
			}
			// And it holds: nothing drifts off to another verdict by itself.
			if c.verdict != proto.VerdictSettling {
				w.run(3 * time.Minute)
				w.want(c.verdict)
			}
		})
	}
}

func TestHintComesFromTheLog(t *testing.T) {
	w := started(t)
	w.run(28 * time.Second)
	w.serverUp = false
	w.run(8 * time.Second)
	w.send(LogHint{Server: "a-DE", Hint: HintKeys})
	w.send(LogHint{Server: "b-NL", Hint: HintUnreachable}) // another server's trouble
	w.until(proto.VerdictServerDown, time.Minute)
	if h := w.m.Diagnosis().Hint; h != HintKeys {
		t.Errorf("подсказка %q, жду %q", h, HintKeys)
	}
	// The line is long gone from the log; the hint stays while the verdict does.
	w.run(5 * time.Minute)
	if h := w.m.Diagnosis().Hint; h != HintKeys {
		t.Errorf("через пять минут подсказка %q, должна была остаться", h)
	}
	w.serverUp = true
	w.until(proto.VerdictOK, time.Minute)
	if h := w.m.Diagnosis().Hint; h != HintNone {
		t.Errorf("у диагноза ok осталась подсказка %q", h)
	}
}

func TestHandshakeCutOnEveryServerLooksLikeBlocking(t *testing.T) {
	w := started(t)
	w.run(28 * time.Second)
	w.serverUp, w.others = false, nil
	w.run(8 * time.Second)
	w.send(LogHint{Server: "a-DE", Hint: HintReset})
	w.send(LogHint{Server: "b-NL", Hint: HintReset})
	w.until(proto.VerdictAllServersDown, time.Minute)
	if h := w.m.Diagnosis().Hint; h != HintBlocked {
		t.Errorf("подсказка %q, жду %q", h, HintBlocked)
	}

	// The same cut on the one server in use, with others alive, is not it.
	w = started(t)
	w.run(28 * time.Second)
	w.serverUp = false
	w.run(8 * time.Second)
	w.send(LogHint{Server: "a-DE", Hint: HintReset})
	w.until(proto.VerdictServerDown, time.Minute)
	if h := w.m.Diagnosis().Hint; h != HintNone {
		t.Errorf("подсказка %q, а оборванное рукопожатие на одном сервере ничего не значит", h)
	}
}

func TestOldLogLinesExplainNothing(t *testing.T) {
	w := started(t)
	w.send(LogHint{Server: "a-DE", Hint: HintAuth})
	w.run(10 * time.Minute)
	w.serverUp = false
	w.until(proto.VerdictServerDown, time.Minute)
	if h := w.m.Diagnosis().Hint; h != HintNone {
		t.Errorf("подсказка %q взята из строки десятиминутной давности", h)
	}
}

func TestNothingIsProbedWhileTheVPNIsOff(t *testing.T) {
	w := newWorld(t)
	w.run(10 * time.Minute)
	w.send(Traffic{})
	w.send(Wake{})
	w.send(Network{Up: false})
	w.run(time.Minute)
	if len(w.probes)+len(w.asked) != 0 {
		t.Errorf("VPN выключен, а зонды ушли: %d и %v", len(w.probes), w.asked)
	}
	w.want(proto.VerdictSettling)
}
