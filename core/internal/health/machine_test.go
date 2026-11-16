package health_test

import (
	"testing"
	"time"

	"strelka/internal/health"
	. "strelka/internal/health/healthtest"
	"strelka/proto"
)

// ---- the scenarios of the health rules ------------------------------------

// The complaint that started all this: one missed ping turned the shield
// yellow and the next good one turned it back, with a notification each way.
func TestOneMissAmongAnswersChangesNothing(t *testing.T) {
	w := Started(t)
	w.Run(5 * time.Minute)

	mark := w.Elapsed()
	w.ServerUp = false
	for n := len(w.Probes); len(w.Probes) == n; {
		w.Run(time.Second)
	}
	w.Run(w.MissTakes) // that one probe is lost; the re-check gets through
	w.ServerUp = true
	w.Run(10 * time.Minute)

	for _, c := range w.Changes {
		if c.At >= mark {
			t.Errorf("после одного промаха диагноз стал %q (%s)", c.V, w.History())
		}
	}
	w.Want(proto.VerdictOK)
}

func TestThreeMissesWithInternetIsServerDown(t *testing.T) {
	for name, takes := range map[string]time.Duration{
		"сервер молчит до таймаута": 5 * time.Second,
		"сервер отказывает сразу":   50 * time.Millisecond,
	} {
		t.Run(name, func(t *testing.T) {
			w := Started(t)
			w.MissTakes = takes
			w.Run(28 * time.Second) // the next calm probe is two seconds away
			w.ServerUp = false
			w.Run(2 * time.Second)
			took := w.Until(proto.VerdictServerDown, time.Minute)

			// Counted from the first probe that did not get through.
			if took < 15*time.Second-takes || took > 20*time.Second {
				t.Errorf("диагноз через %v после начала проблемы, жду 15–20 с (%s)", took, w.History())
			}
			d := w.M.Diagnosis()
			if !d.Internet || !d.Network || d.Server != "a-DE" || len(d.Alive) != 2 || d.Alive[0] != "b-NL" {
				t.Errorf("диагноз: %+v", d)
			}
			for _, c := range w.Changes {
				if c.V == proto.VerdictNoInternet || c.V == proto.VerdictAllServersDown || c.V == proto.VerdictSettling && c.At > 3*time.Second {
					t.Errorf("по пути был диагноз %q (%s)", c.V, w.History())
				}
			}
		})
	}
}

func TestSameMissesWithoutInternetIsNoInternet(t *testing.T) {
	w := Started(t)
	w.Run(28 * time.Second)
	w.Internet = false
	w.Until(proto.VerdictNoInternet, time.Minute)
	d := w.M.Diagnosis()
	if d.Internet || !d.Network {
		t.Errorf("internet=%v network=%v, жду false и true", d.Internet, d.Network)
	}
	if w.Saw(proto.VerdictServerDown, 0) || w.Saw(proto.VerdictAllServersDown, 0) {
		t.Errorf("сервер обвинили, хотя не было интернета: %s", w.History())
	}
	for _, a := range w.Asked {
		if a == "others" {
			t.Error("другие серверы проверялись без интернета")
		}
	}

	// The internet comes back and so does the server: no detour through
	// "server down" on the way to "ok".
	w.Internet = true
	w.Until(proto.VerdictOK, time.Minute)
	if w.Saw(proto.VerdictServerDown, 0) {
		t.Errorf("после возвращения интернета сервер объявили мёртвым: %s", w.History())
	}
}

func TestInternetBackButServerStillSilentBecomesServerDown(t *testing.T) {
	w := Started(t)
	w.Run(28 * time.Second)
	w.Internet, w.ServerUp = false, false
	w.Until(proto.VerdictNoInternet, time.Minute)
	w.Internet = true
	w.Until(proto.VerdictServerDown, time.Minute)
}

func TestNoServerAnsweringIsAllServersDown(t *testing.T) {
	w := Started(t)
	w.Run(28 * time.Second)
	w.ServerUp, w.Others = false, nil
	w.Until(proto.VerdictAllServersDown, time.Minute)
	if d := w.M.Diagnosis(); !d.Internet || len(d.Alive) != 0 {
		t.Errorf("диагноз: %+v", d)
	}

	// One of them wakes up: the picture is refreshed while the trouble lasts.
	w.Others = []string{"c-FI"}
	w.Until(proto.VerdictServerDown, 2*time.Minute)
}

// Waking from sleep: the network is not back yet and every probe is refused
// at once. Whatever was counted before the lid closed must not add up.
func TestSleepForAnHourIsSettlingNotAnOutage(t *testing.T) {
	w := Started(t)
	w.Run(28 * time.Second)
	w.ServerUp = false
	w.Run(9 * time.Second) // two misses on record when the lid closes

	w.Sleep(time.Hour)
	mark := w.Elapsed()
	w.Want(proto.VerdictSettling)

	w.Run(10 * time.Second)
	w.Want(proto.VerdictSettling)
	if n := len(w.Probes); n > 0 && w.Probes[n-1].At >= mark {
		t.Error("зонд ушёл в первые секунды после пробуждения")
	}
	w.ServerUp = true // the network is back by the time the pause ends
	w.Until(proto.VerdictOK, 10*time.Second)
	for _, c := range w.Changes {
		if c.At >= mark && c.V != proto.VerdictSettling && c.V != proto.VerdictOK {
			t.Errorf("после сна был диагноз %q (%s)", c.V, w.History())
		}
	}
}

func TestTrafficOutweighsAMissedProbe(t *testing.T) {
	w := Started(t)
	w.Run(28 * time.Second)
	w.ServerUp = false // the probe target is unreachable, the server is fine
	mark := w.Elapsed()
	// Traffic that starts only after the first miss: the miss is forgotten.
	w.Run(4 * time.Second)
	w.Traffic(3 * time.Minute)
	for _, c := range w.Changes {
		if c.At >= mark {
			t.Errorf("при живом трафике диагноз стал %q (%s)", c.V, w.History())
		}
	}
	w.Want(proto.VerdictOK)
}

func TestCalmProbeIsSkippedWhileTrafficFlows(t *testing.T) {
	w := Started(t)
	before := len(w.Probes)
	w.Traffic(5 * time.Minute)
	if n := len(w.Probes) - before; n != 0 {
		t.Errorf("за пять минут с трафиком ушло %d зондов, жду ни одного", n)
	}
	w.Run(2 * time.Minute)
	if n := len(w.Probes) - before; n < 3 || n > 4 {
		t.Errorf("за две минуты тишины ушло %d зондов, жду по одному в 30 с", n)
	}
}

func TestRetriesComeQuicklyAndAlternateAddresses(t *testing.T) {
	w := Started(t)
	w.MissTakes = 10 * time.Millisecond
	w.Run(28 * time.Second)
	w.ServerUp = false
	from := len(w.Probes)
	w.Until(proto.VerdictServerDown, time.Minute)

	got := w.Probes[from:]
	if len(got) < 5 {
		t.Fatalf("зондов до диагноза: %d, жду пять (промах, +2, +5, +10, +15)", len(got))
	}
	first := got[0].At
	for i, want := range []time.Duration{0, 2 * time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second} {
		// The machine is asked once a second, so a probe may leave late by that much.
		if off := got[i].At - first; off < want || off > want+1100*time.Millisecond {
			t.Errorf("зонд %d ушёл через %v после первого, жду %v", i+1, off, want)
		}
		if i > 0 && got[i].URL == got[i-1].URL {
			t.Errorf("зонды %d и %d ушли на один адрес %s", i, i+1, got[i].URL)
		}
	}
	// Three quick refusals are not an outage: the evidence must span 12 s.
	if w.Changes[len(w.Changes)-1].At-first < 12*time.Second {
		t.Errorf("диагноз раньше, чем через 12 с после первого промаха (%s)", w.History())
	}
}

func TestRecoveryTakesTwoAnswersOrTraffic(t *testing.T) {
	w := Started(t)
	w.Run(28 * time.Second)
	w.ServerUp = false
	w.Until(proto.VerdictServerDown, time.Minute)

	w.ServerUp = true
	took := w.Until(proto.VerdictOK, time.Minute)
	if took > 13*time.Second {
		t.Errorf("сервер ожил, а диагноз ok только через %v", took)
	}
	answers := 0
	for i := len(w.Probes) - 1; i >= 0 && w.Probes[i].At > w.Elapsed()-took; i-- {
		answers++
	}
	if answers != 2 {
		t.Errorf("до ok ушло %d зондов, жду два ответа подряд", answers)
	}

	// Down again; this time real traffic brings it back at once.
	w.Run(20 * time.Second)
	w.ServerUp = false
	w.Until(proto.VerdictServerDown, 2*time.Minute)
	w.Send(health.Traffic{})
	w.Want(proto.VerdictOK)
}

func TestNetworkGoneIsNoNetworkAndComingBackSettles(t *testing.T) {
	w := Started(t)
	w.Run(10 * time.Second)
	w.ServerUp, w.Internet = false, false
	w.Send(health.Network{Up: false})
	w.Want(proto.VerdictSettling) // Wi-Fi may just be hopping to another point
	w.Run(14 * time.Second)
	w.Want(proto.VerdictSettling)
	w.Run(2 * time.Second)
	w.Want(proto.VerdictNoNetwork)
	if d := w.M.Diagnosis(); d.Network || d.Internet {
		t.Errorf("network=%v internet=%v, жду false", d.Network, d.Internet)
	}
	w.Run(5 * time.Minute)
	w.Want(proto.VerdictNoNetwork)

	w.ServerUp, w.Internet = true, true
	w.Send(health.Network{Up: true})
	w.Want(proto.VerdictSettling)
	took := w.Until(proto.VerdictOK, 30*time.Second)
	if took < 15*time.Second {
		t.Errorf("ok через %v после смены сети, раньше 15 с диагнозов быть не должно", took)
	}
}

// The monitor can be wrong (an exotic adapter it does not count as a route).
// A server that answers is stronger evidence than a routing table.
func TestAnAnswerOverrulesTheMonitor(t *testing.T) {
	w := Started(t)
	w.Send(health.Network{Up: false})
	w.Until(proto.VerdictOK, 30*time.Second)
	if d := w.M.Diagnosis(); !d.Network {
		t.Error("сервер отвечает, а network=false")
	}
}

// A hand-picked server must not inherit the green shield of the previous
// one: until its first probe nothing is known about it.
func TestSwitchingStartsOver(t *testing.T) {
	w := Started(t)
	w.Run(10 * time.Second)

	w.ServerUp = false
	mark := w.Elapsed()
	w.Send(health.Select{Server: "b-NL"})
	w.Want(proto.VerdictSettling)
	took := w.Until(proto.VerdictServerDown, time.Minute)
	if took > 20*time.Second {
		t.Errorf("мёртвый сервер после переключения: диагноз через %v, жду до 20 с", took)
	}
	for _, c := range w.Changes {
		if c.At > mark && c.V == proto.VerdictOK {
			t.Errorf("сервер, который ни разу не ответил, был %q (%s)", c.V, w.History())
		}
	}
	if d := w.M.Diagnosis(); d.Server != "b-NL" {
		t.Errorf("диагноз о сервере %q, жду b-NL", d.Server)
	}

	w.ServerUp = true
	w.Send(health.Select{Server: "c-FI"})
	w.Want(proto.VerdictSettling)
	if took := w.Until(proto.VerdictOK, 5*time.Second); took > time.Second {
		t.Errorf("живой сервер после переключения: ok через %v, жду с первым же ответом", took)
	}
}

func TestAnswersToOldQuestionsAreDropped(t *testing.T) {
	w := Started(t)
	w.Run(28 * time.Second)
	w.ServerUp = false
	w.Run(2500 * time.Millisecond) // a probe of a-DE is out and will fail
	if len(w.Pending) == 0 {
		t.Fatal("в полёте нет ни одного зонда")
	}
	w.ServerUp = true
	stale := w.Pending[0]
	w.Pending = nil
	w.Send(health.Select{Server: "b-NL"}) // sends its own probe, answered below
	w.Now = w.Now.Add(4 * time.Second)
	w.Send(stale.Ev) // the old server's miss arrives late
	w.Send(health.ServerResult{Gen: stale.Ev.(health.ServerResult).Gen, OK: true, Ms: 1})
	w.Want(proto.VerdictSettling)
	if d := w.M.Diagnosis(); !d.PingAt.IsZero() {
		t.Errorf("ответ прежнего сервера записан новому: пинг %d", d.Ping)
	}
}

func TestEnginePhasesMapToVerdicts(t *testing.T) {
	w := NewWorld(t)
	w.Want(proto.VerdictSettling) // off
	w.Send(health.Phase{Phase: proto.PhaseStarting})
	w.Want(proto.VerdictSettling)
	w.Send(health.Phase{Phase: proto.PhaseOn})
	w.Want(proto.VerdictSettling)
	w.Run(time.Second)
	w.Want(proto.VerdictOK)

	w.Send(health.Phase{Phase: proto.PhaseRecovering})
	w.Want(proto.VerdictEngineRecovering)
	w.Run(30 * time.Second)
	if n := len(w.Probes); n != 1 {
		t.Errorf("зондов %d: пока движок лежит, проверять нечем", n)
	}
	w.Send(health.Phase{Phase: proto.PhaseOn})
	w.Want(proto.VerdictSettling)
	w.Run(time.Second)
	w.Want(proto.VerdictOK)

	w.Send(health.Phase{Phase: proto.PhaseFailed, Reason: proto.FailTUN})
	w.Want(proto.VerdictEngineFailed)
	if d := w.M.Diagnosis(); d.Reason != proto.FailTUN {
		t.Errorf("причина %q, жду %q", d.Reason, proto.FailTUN)
	}
	w.Send(health.Phase{Phase: proto.PhaseOff})
	w.Want(proto.VerdictSettling)
}

func TestHelperDownHidesServerVerdicts(t *testing.T) {
	w := Started(t)
	w.ServerUp = false // the server goes through the helper
	w.Send(health.Helper{Down: true})
	w.Want(proto.VerdictEngineRecovering) // it is being restarted; a blip is not news
	w.Run(9 * time.Second)
	w.Want(proto.VerdictEngineRecovering)
	w.Run(2 * time.Minute)
	w.Want(proto.VerdictHelperDown)
	if w.Saw(proto.VerdictServerDown, 0) {
		t.Errorf("виноват помощник, а не сервер: %s", w.History())
	}
	w.ServerUp = true
	w.Send(health.Helper{Down: false})
	w.Want(proto.VerdictSettling)
	w.Until(proto.VerdictOK, 5*time.Second)

	// A helper the daemon gave up on is down at once.
	w.Send(health.Helper{Down: true, Gone: true})
	w.Want(proto.VerdictHelperDown)
}

func TestEveryOtherProbeLostIsDegradedNotDown(t *testing.T) {
	w := Started(t)
	w.MissTakes = 100 * time.Millisecond
	for i := 0; i < 12 && w.Verdict() != proto.VerdictDegraded; i++ {
		// One lost probe and its quick re-check that gets through.
		w.ServerUp = false
		for n := len(w.Probes); len(w.Probes) == n; {
			w.Run(time.Second)
		}
		w.Run(500 * time.Millisecond)
		w.ServerUp = true
		w.Run(10 * time.Second)
	}
	w.Want(proto.VerdictDegraded)
	if w.Saw(proto.VerdictServerDown, 0) {
		t.Errorf("сервер отвечал через раз, а его объявили мёртвым: %s", w.History())
	}

	// It takes a clean run to be "ok" again, not the first good answer.
	w.Run(time.Minute)
	w.Want(proto.VerdictDegraded)
	w.Until(proto.VerdictOK, 15*time.Minute)
}

func TestSlowAnswersForTwoMinutesAreDegraded(t *testing.T) {
	w := Started(t)
	w.Run(5 * time.Minute) // learns that 80 ms is usual

	w.Ping = 200 // more than twice the usual, but under the floor
	w.Run(5 * time.Minute)
	w.Want(proto.VerdictOK)

	w.Ping = 80
	w.Run(3 * time.Minute)
	w.Ping = 1500
	w.Run(90 * time.Second)
	w.Want(proto.VerdictOK) // slow, but not for long enough yet
	w.Until(proto.VerdictDegraded, 2*time.Minute)

	w.Ping = 80
	w.Until(proto.VerdictOK, time.Minute)
}

func TestOutageDoesNotLeaveDegradedBehind(t *testing.T) {
	w := Started(t)
	w.Run(28 * time.Second)
	w.ServerUp = false
	w.Until(proto.VerdictServerDown, time.Minute)
	w.Run(3 * time.Minute)
	w.ServerUp = true
	w.Until(proto.VerdictOK, time.Minute)
	w.Run(10 * time.Minute)
	if w.Saw(proto.VerdictDegraded, 0) {
		t.Errorf("после обычного падения и подъёма был диагноз degraded: %s", w.History())
	}
}

// Each verdict, and the least set of signals that leads to it.
func TestEveryVerdictHasItsSignals(t *testing.T) {
	cases := []struct {
		verdict string
		arrange func(w *World)
	}{
		{proto.VerdictOK, func(w *World) {}},
		{proto.VerdictSettling, func(w *World) { w.Send(health.Select{Server: "b-NL"}) }},
		{proto.VerdictServerDown, func(w *World) { w.ServerUp = false }},
		{proto.VerdictAllServersDown, func(w *World) { w.ServerUp, w.Others = false, nil }},
		{proto.VerdictNoInternet, func(w *World) { w.Internet = false }},
		{proto.VerdictNoNetwork, func(w *World) { w.Internet = false; w.Send(health.Network{Up: false}) }},
		{proto.VerdictEngineRecovering, func(w *World) { w.Send(health.Phase{Phase: proto.PhaseRecovering}) }},
		{proto.VerdictEngineFailed, func(w *World) { w.Send(health.Phase{Phase: proto.PhaseFailed, Reason: proto.FailCrashLoop}) }},
		{proto.VerdictHelperDown, func(w *World) { w.Send(health.Helper{Down: true}) }},
	}
	for _, c := range cases {
		t.Run(c.verdict, func(t *testing.T) {
			w := Started(t)
			c.arrange(w)
			if w.Verdict() != c.verdict {
				w.Until(c.verdict, 2*time.Minute)
			}
			// And it holds: nothing drifts off to another verdict by itself.
			if c.verdict != proto.VerdictSettling {
				w.Run(3 * time.Minute)
				w.Want(c.verdict)
			}
		})
	}
}

func TestHintComesFromTheLog(t *testing.T) {
	w := Started(t)
	w.Run(28 * time.Second)
	w.ServerUp = false
	w.Run(8 * time.Second)
	w.Send(health.LogHint{Server: "a-DE", Hint: health.HintKeys})
	w.Send(health.LogHint{Server: "b-NL", Hint: health.HintUnreachable}) // another server's trouble
	w.Until(proto.VerdictServerDown, time.Minute)
	if h := w.M.Diagnosis().Hint; h != health.HintKeys {
		t.Errorf("подсказка %q, жду %q", h, health.HintKeys)
	}
	// The line is long gone from the log; the hint stays while the verdict does.
	w.Run(5 * time.Minute)
	if h := w.M.Diagnosis().Hint; h != health.HintKeys {
		t.Errorf("через пять минут подсказка %q, должна была остаться", h)
	}
	w.ServerUp = true
	w.Until(proto.VerdictOK, time.Minute)
	if h := w.M.Diagnosis().Hint; h != health.HintNone {
		t.Errorf("у диагноза ok осталась подсказка %q", h)
	}
}

func TestHandshakeCutOnEveryServerLooksLikeBlocking(t *testing.T) {
	w := Started(t)
	w.Run(28 * time.Second)
	w.ServerUp, w.Others = false, nil
	w.Run(8 * time.Second)
	w.Send(health.LogHint{Server: "a-DE", Hint: health.HintReset})
	w.Send(health.LogHint{Server: "b-NL", Hint: health.HintReset})
	w.Until(proto.VerdictAllServersDown, time.Minute)
	if h := w.M.Diagnosis().Hint; h != health.HintBlocked {
		t.Errorf("подсказка %q, жду %q", h, health.HintBlocked)
	}

	// The same cut on the one server in use, with others alive, is not it.
	w = Started(t)
	w.Run(28 * time.Second)
	w.ServerUp = false
	w.Run(8 * time.Second)
	w.Send(health.LogHint{Server: "a-DE", Hint: health.HintReset})
	w.Until(proto.VerdictServerDown, time.Minute)
	if h := w.M.Diagnosis().Hint; h != health.HintNone {
		t.Errorf("подсказка %q, а оборванное рукопожатие на одном сервере ничего не значит", h)
	}
}

func TestOldLogLinesExplainNothing(t *testing.T) {
	w := Started(t)
	w.Send(health.LogHint{Server: "a-DE", Hint: health.HintAuth})
	w.Run(10 * time.Minute)
	w.ServerUp = false
	w.Until(proto.VerdictServerDown, time.Minute)
	if h := w.M.Diagnosis().Hint; h != health.HintNone {
		t.Errorf("подсказка %q взята из строки десятиминутной давности", h)
	}
}

func TestNothingIsProbedWhileTheVPNIsOff(t *testing.T) {
	w := NewWorld(t)
	w.Run(10 * time.Minute)
	w.Send(health.Traffic{})
	w.Send(health.Wake{})
	w.Send(health.Network{Up: false})
	w.Run(time.Minute)
	if len(w.Probes)+len(w.Asked) != 0 {
		t.Errorf("VPN выключен, а зонды ушли: %d и %v", len(w.Probes), w.Asked)
	}
	w.Want(proto.VerdictSettling)
}
