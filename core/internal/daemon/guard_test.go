package daemon

import (
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strelka/internal/engine"
	"strelka/internal/guard"
	"strelka/proto"
)

// guarded is a rig whose daemon has a kill switch - a fake one: these tests
// are about when the daemon reaches for the guard, not about the firewall.
type guarded struct {
	*rig
	fake *guard.Fake
	// tun is the id of the tunnel adapter the daemon will "find"; 0 is none.
	tun atomic.Uint64
	// atLaunch is what the guard had been told when the engine was first
	// about to start.
	atLaunch atomic.Pointer[[]string]
	// naked: a state was seen with the VPN not off and the guard not engaged.
	naked string
}

func startGuarded(t *testing.T, script string, fake *guard.Fake) *guarded {
	t.Helper()
	g := &guarded{fake: fake}
	g.tun.Store(77)
	g.rig = startRigWith(t, newData(t), script, func(o *Options) {
		quickHealth(o)
		o.Guard = fake
		o.FindTunnel = func() (uint64, bool) { v := g.tun.Load(); return v, v != 0 }
		launch := o.Launch
		o.Launch = func(cfg string) engine.Launch {
			calls := fake.Calls()
			g.atLaunch.CompareAndSwap(nil, &calls)
			return launch(cfg)
		}
	})
	return g
}

// wait is waitState that also keeps an eye on the rule of the whole stage:
// while the VPN is anything but off and the setting is on, the guard is up.
func (g *guarded) wait(what string, ok func(proto.State) bool) {
	g.t.Helper()
	g.waitState(what, func(st proto.State) bool {
		if st.VPN.Phase != proto.PhaseOff && st.Settings.Guard && !st.Guard.Engaged && g.naked == "" {
			g.naked = st.VPN.Phase
		}
		return ok(st)
	})
}

func (g *guarded) waitCall(call string) {
	g.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !contains(g.fake.Calls(), call); {
		if time.Now().After(deadline) {
			g.t.Fatalf("замку не сказали %q; сказали: %v", call, g.fake.Calls())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func count(list []string, prefix string) int {
	n := 0
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			n++
		}
	}
	return n
}

func TestGuardGoesUpBeforeTheEngineAndComesDownAfterIt(t *testing.T) {
	g := startGuarded(t, "ok", &guard.Fake{})
	if len(g.fake.Calls()) != 0 || g.last.Guard.Engaged {
		t.Fatalf("VPN выключен, а замок тронут: %v", g.fake.Calls())
	}

	g.call(proto.MethodVPNStart, nil, nil)
	g.wait("включён", func(st proto.State) bool { return st.VPN.Phase == proto.PhaseOn })
	if at := g.atLaunch.Load(); at == nil || len(*at) == 0 || !strings.HasPrefix((*at)[0], "engage lan=true apps=3") {
		t.Fatalf("к запуску движка замку сказали %v, жду включение", at)
	}
	g.waitCall("tunnel 77")
	if !g.last.Guard.Engaged || g.last.Guard.Blocking {
		t.Errorf("guard = %+v", g.last.Guard)
	}
	var st proto.GuardStatusResult
	g.call(proto.MethodGuardStatus, nil, &st)
	if !st.Engaged || st.Filters == 0 || st.TunLUID != 77 {
		t.Errorf("guard.status = %+v", st)
	}

	g.call(proto.MethodVPNStop, nil, nil)
	g.wait("выключен", func(st proto.State) bool { return st.VPN.Phase == proto.PhaseOff })
	if g.last.Guard.Engaged {
		t.Error("VPN выключен, а замок остался")
	}
	calls := g.fake.Calls()
	if n := len(calls); n < 2 || calls[n-1] != "release" || calls[n-2] != "tunnel 0" {
		t.Errorf("конец: %v, жду снятие разрешения адаптера и потом замка", calls)
	}
	if count(calls, "release") != 1 || count(calls, "engage") != 1 {
		t.Errorf("замок дёргали лишний раз: %v", calls)
	}
	if g.naked != "" {
		t.Errorf("было состояние %q без замка", g.naked)
	}
}

// A crash is exactly when the guard must stay: only the tunnel's permit goes
// and comes back, with the new adapter's id.
func TestGuardStaysThroughACrash(t *testing.T) {
	g := startGuarded(t, "ok,ok", &guard.Fake{})
	g.call(proto.MethodVPNStart, nil, nil)
	g.wait("включён", func(st proto.State) bool { return st.VPN.Phase == proto.PhaseOn })
	g.waitCall("tunnel 77")

	pid := g.engine().PID
	g.tun.Store(78) // the next adapter is a new one
	killProcess(t, pid)
	g.wait("упал", func(st proto.State) bool { return st.VPN.Phase == proto.PhaseRecovering })
	g.wait("снова включён", func(st proto.State) bool { return st.VPN.Phase == proto.PhaseOn })
	g.waitCall("tunnel 78")
	calls := g.fake.Calls()
	if count(calls, "release") != 0 || count(calls, "engage") != 1 {
		t.Errorf("падение движка тронуло замок: %v", calls)
	}
	if g.naked != "" {
		t.Errorf("было состояние %q без замка", g.naked)
	}
}

// The whole point, end to end: an engine that will not come up leaves the
// internet closed, the user is told with a way out, and the way out works.
func TestEngineThatStaysDownClosesTheInternetUntilReleased(t *testing.T) {
	g := startGuarded(t, "tun,tun,tun,tun,tun,ok", &guard.Fake{})
	g.call(proto.MethodVPNStart, nil, nil)
	g.wait("интернет закрыт", func(st proto.State) bool { return st.Guard.Blocking && len(st.Incidents) == 1 })
	if g.last.VPN.Phase == proto.PhaseOn || !g.last.Guard.Engaged {
		t.Fatalf("phase=%s guard=%+v", g.last.VPN.Phase, g.last.Guard)
	}
	g.wait("движок сдался", func(st proto.State) bool { return st.VPN.Phase == proto.PhaseFailed })
	g.settle(200 * time.Millisecond)
	inc := g.last.Incidents[0]
	if !g.last.Guard.Blocking || inc.Severity != proto.SeverityCritical || inc.Title != "Интернет закрыт, чтобы трафик не пошёл мимо VPN" ||
		!strings.HasPrefix(inc.Body, "VPN не запускается: не создаётся сетевой адаптер") {
		t.Errorf("guard=%+v, инцидент %+v", g.last.Guard, inc)
	}
	if len(g.toasts) == 0 {
		t.Fatal("об интернете, который закрыт, не сообщили")
	}
	for _, toast := range g.toasts {
		if toast.ID != inc.ID || !toast.Sticky || len(toast.Actions) != 2 || toast.Actions[1].ID != proto.ActionRelease {
			t.Errorf("уведомление: %+v", toast)
		}
	}
	if count(g.fake.Calls(), "release") != 0 {
		t.Fatalf("замок снялся сам: %v", g.fake.Calls())
	}

	g.call(proto.MethodIncidentAction, proto.IncidentActionParams{ID: inc.ID, Action: proto.ActionRelease}, nil)
	g.wait("интернет открыт", func(st proto.State) bool {
		return st.VPN.Phase == proto.PhaseOff && !st.Guard.Engaged && !st.Guard.Blocking && len(st.Incidents) == 0
	})
	if g.last.VPN.Desired != proto.DesiredOff {
		t.Errorf("desired = %s после снятия замка", g.last.VPN.Desired)
	}
	g.settle(100 * time.Millisecond)
	if calls := g.fake.Calls(); calls[len(calls)-1] != "release" {
		t.Errorf("замок не снят: %v", calls)
	}
	if last := g.toasts[len(g.toasts)-1]; !last.Close || last.ID != inc.ID {
		t.Errorf("уведомление не убрано: %s", g.toastTitles())
	}
	if g.naked != "" {
		t.Errorf("было состояние %q без замка", g.naked)
	}
}

func TestClosedInternetOpensWhenTheEngineComesUp(t *testing.T) {
	g := startGuarded(t, "tun,tun,tun,tun,tun,ok", &guard.Fake{})
	g.call(proto.MethodVPNStart, nil, nil)
	g.wait("движок сдался, интернет закрыт", func(st proto.State) bool {
		return st.VPN.Phase == proto.PhaseFailed && st.Guard.Blocking && len(st.Incidents) == 1
	})
	id := g.last.Incidents[0].ID
	g.call(proto.MethodIncidentAction, proto.IncidentActionParams{ID: id, Action: proto.ActionRetry}, nil)
	g.wait("VPN поднялся", func(st proto.State) bool {
		return st.VPN.Phase == proto.PhaseOn && !st.Guard.Blocking && st.Guard.Engaged && len(st.Incidents) == 0
	})
	g.settle(200 * time.Millisecond)
	if last := g.toasts[len(g.toasts)-1]; last.Title != "Снова работает" || last.ID != id {
		t.Errorf("уведомления: %s", g.toastTitles())
	}
	if n := count(g.fake.Calls(), "release"); n != 0 {
		t.Errorf("замок снимали %d раз", n)
	}
}

func TestGuardFollowsTheSettingsWithoutARestart(t *testing.T) {
	g := startGuarded(t, "ok", &guard.Fake{})
	g.call(proto.MethodVPNStart, nil, nil)
	g.wait("включён", func(st proto.State) bool { return st.VPN.Phase == proto.PhaseOn })
	g.waitCall("tunnel 77")
	pid := g.engine().PID
	set := func(patch string) {
		t.Helper()
		g.call(proto.MethodSettingsSet, proto.SettingsSetParams{Patch: []byte(patch)}, nil)
	}

	set("{\"allowLan\": false}")
	g.waitCall("engage lan=false apps=3")
	if n := count(g.fake.Calls(), "release"); n != 0 {
		t.Fatalf("смена настройки сняла замок: %v", g.fake.Calls())
	}

	set("{\"guard\": false}")
	g.waitState("замок выключен", func(st proto.State) bool { return !st.Guard.Enabled && !st.Guard.Engaged })
	g.waitCall("release")

	set("{\"guard\": true}")
	g.waitState("замок включён", func(st proto.State) bool { return st.Guard.Engaged })
	if n := count(g.fake.Calls(), "engage"); n != 3 {
		t.Errorf("включений замка %d, жду 3: %v", n, g.fake.Calls())
	}
	// The adapter was there all along: its permit comes back with the guard.
	var st proto.GuardStatusResult
	g.call(proto.MethodGuardStatus, nil, &st)
	if st.TunLUID != 77 {
		t.Errorf("guard.status после включения = %+v", st)
	}
	if g.last.VPN.Phase != proto.PhaseOn || g.engine().PID != pid {
		t.Error("настройки замка перезапустили движок")
	}
}

// A system that refuses the filters must not cost the user the VPN as well.
func TestVPNStartsEvenIfTheGuardWillNot(t *testing.T) {
	g := startGuarded(t, "ok", &guard.Fake{Err: errors.New("access denied")})
	g.call(proto.MethodVPNStart, nil, nil)
	g.waitPhase(proto.PhaseOn)
	g.settle(300 * time.Millisecond)
	if g.last.Guard.Engaged || g.last.Guard.Blocking || !g.last.Guard.Enabled {
		t.Errorf("guard = %+v", g.last.Guard)
	}
	said := 0
	for _, e := range g.journal {
		if e.Cat == proto.CatGuard && strings.Contains(e.Text, "Замок не включился") {
			said++
		}
	}
	if said != 1 {
		t.Errorf("о замке, который не включился, сказано %d раз, жду один", said)
	}
	// Refused once, it is not hammered on every tick.
	if n := count(g.fake.Calls(), "engage-failed"); n != 1 {
		t.Errorf("попыток включить замок: %d", n)
	}
}

// desired = on with "start with the system" off: the daemon comes up with
// the VPN down and nothing to bring up. A guard here would be a closed
// internet nobody asked for.
func TestGuardIsNotEngagedForAVPNThatIsNotStarted(t *testing.T) {
	paths := newData(t)
	os.WriteFile(paths.DesiredFile(), []byte("{\"desired\":\"on\"}"), 0o600)
	os.WriteFile(paths.SettingsFile(), []byte("{\"guard\":true,\"startOnBoot\":false}"), 0o600)
	fake := &guard.Fake{}
	r := startRigWith(t, paths, "ok", func(o *Options) {
		quickHealth(o)
		o.Guard = fake
		o.FindTunnel = func() (uint64, bool) { return 0, false }
	})
	r.settle(200 * time.Millisecond)
	if r.last.VPN.Phase != proto.PhaseOff || r.last.Guard.Engaged || len(fake.Calls()) != 0 {
		t.Errorf("phase=%s guard=%+v, замку сказали %v", r.last.VPN.Phase, r.last.Guard, fake.Calls())
	}
}
