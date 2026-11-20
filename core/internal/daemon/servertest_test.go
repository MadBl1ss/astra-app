package daemon

import (
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"strelka/internal/engine/enginetest"
	"strelka/internal/profile"
	"strelka/internal/subs"
	"strelka/proto"
)

// Favourites first, then every subscription in turn, then servers added by
// link: the order the window lists them in, so its top answers first.
func TestListOrderFollowsTheWindow(t *testing.T) {
	srv := func(id, sub string) profile.Server { return profile.Server{Server: subs.Server{ID: id}, Sub: sub} }
	p := &profile.Profile{
		Subs:      []profile.Sub{{ID: "s2"}, {ID: "s1"}},
		Servers:   []profile.Server{srv("a-DE", "s1"), srv("Home", ""), srv("b-NL", "s2"), srv("a-FI", "s1"), srv("b-SE", "s2")},
		Favorites: []string{"a-FI", "gone", "Home"},
	}
	want := []string{"a-FI", "Home", "b-NL", "b-SE", "a-DE"}
	if got := listOrder(p); !reflect.DeepEqual(got, want) {
		t.Errorf("порядок проверки:\n есть %v\n жду  %v", got, want)
	}
}

// slowLinks makes n servers that hold a test and fail it, like dead ones.
func slowLinks(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = link(fmt.Sprintf("slow%02d", i), fmt.Sprintf("slow%02d.example.com", i))
	}
	return out
}

// With the VPN off the test brings an engine of its own: the pings come in,
// the VPN stays off, and the engine and its configs are gone afterwards.
func TestServerTestWithTheVPNOff(t *testing.T) {
	paths := emptyData(t)
	// The parser sends servers to Xray only when the helper is installed.
	if err := os.WriteFile(paths.Engine(xrayName), []byte("stand-in"), 0o700); err != nil {
		t.Fatal(err)
	}
	reality := "vless://11111111-1111-4111-8111-111111111111@se.example.com:443?security=reality&sni=www.example.org&pbk=TWFkZVVw&sid=ab#[SE] made up"
	sub := newSubServer(t, link("DE", "de.example.com"), link("dead", "gone.example.com"), reality)
	r := startRig(t, paths, "ok")
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "a"}, nil); err != nil {
		t.Fatal(err)
	}

	r.call(proto.MethodServerTest, nil, nil)
	saw := map[string]bool{}
	r.waitState("конец проверки через свой движок", func(st proto.State) bool {
		if st.VPN.Phase != proto.PhaseOff {
			t.Fatalf("проверка включила VPN: %s", st.VPN.Phase)
		}
		if st.Test != nil {
			saw[st.Test.Stage] = true
			saw["own"] = saw["own"] || st.Test.Own
			for _, s := range st.Servers {
				if s.Check != nil {
					saw[*s.Check] = true
				}
			}
		}
		return st.Test == nil && st.Busy == nil && r.journalHas(proto.LevelInfo, "отвечают 2 из 3")
	})
	if !saw["own"] || !saw[proto.TestPreparing] || !saw[proto.CheckQueued] {
		t.Errorf("ход проверки не показан: %v", saw)
	}
	for _, s := range r.last.Servers {
		if s.Check != nil {
			t.Errorf("после проверки у %s остался check=%s", s.ID, *s.Check)
		}
		alive := s.ID != "a-dead"
		if s.Alive != alive || s.PingAt == nil || (alive && (s.Ping == nil || *s.Ping != enginetest.DelayOf(s.ID))) {
			t.Errorf("%s: alive=%v ping=%v pingAt=%v", s.ID, s.Alive, s.Ping, s.PingAt)
		}
	}
	r.eventually("проверочный движок убрал за собой конфиги", func() bool {
		_, e1 := os.Stat(scratch(paths.RunConfig(), "probe"))
		_, e2 := os.Stat(scratch(paths.XrayConfig(), "probe"))
		return os.IsNotExist(e1) && os.IsNotExist(e2)
	})
}

// The bug this all started from: one deadline for the whole list, so the
// servers at its end were never tried. Here thirty silent servers come
// first, and the three that work, at the very end, must still come out alive.
func TestServerTestReachesTheEndOfALongList(t *testing.T) {
	first := newSubServer(t, slowLinks(30)...)
	last := newSubServer(t, link("DE", "de.example.com"), link("NL", "nl.example.com"), link("FI", "fi.example.com"))
	r := startRig(t, emptyData(t), "ok")
	// In this order: the silent subscription first, the working one last.
	for _, sub := range []struct {
		name string
		s    *subServer
	}{{"a", first}, {"b", last}} {
		if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.s.URL, Name: sub.name}, nil); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	r.call(proto.MethodServerTest, nil, nil)
	r.waitState("конец проверки", func(st proto.State) bool { return st.Test == nil && st.Busy == nil && len(st.Servers) == 33 })
	if !r.journalHas(proto.LevelInfo, "отвечают 3 из 33") {
		t.Errorf("итог проверки: %+v", r.journal)
	}
	if r.last.Subs[1].Name != "b" {
		t.Fatalf("подписки не в том порядке: %+v", r.last.Subs)
	}
	for _, s := range r.last.Servers {
		if s.Sub == r.last.Subs[1].ID && !s.Alive {
			t.Errorf("сервер %s в конце списка не проверен или записан мёртвым", s.ID)
		}
	}
	// 30 servers holding 300 ms each, 16 at a time: two waves, not thirty.
	if took := time.Since(started); took > 30*enginetest.SlowFail/2 {
		t.Errorf("проверка шла %v: серверы проверялись не параллельно", took)
	}
}

// Stopping keeps what has been tested and leaves the rest as it was.
func TestServerTestStops(t *testing.T) {
	sub := newSubServer(t, slowLinks(40)...)
	r := startRig(t, emptyData(t), "ok")
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "a"}, nil); err != nil {
		t.Fatal(err)
	}
	r.call(proto.MethodServerTest, nil, nil)
	r.waitState("проверка идёт", func(st proto.State) bool { return st.Test != nil && st.Test.Done > 0 })
	r.call(proto.MethodServerTestStop, nil, nil)
	st := r.state()
	if st.Test != nil || st.Busy != nil {
		t.Fatalf("проверка не остановилась: test=%+v busy=%v", st.Test, deref(st.Busy))
	}
	untested := 0
	for _, s := range st.Servers {
		if s.Check != nil {
			t.Errorf("%s: check=%s после остановки", s.ID, *s.Check)
		}
		if s.PingAt == nil {
			untested++
		}
	}
	if untested == 0 {
		t.Error("остановка дошла до конца списка: нечего было останавливать")
	}
	r.waitState("журнал", func(proto.State) bool {
		return r.journalHas(proto.LevelInfo, "Проверка серверов остановлена. Проверено")
	})
	// A second stop with nothing to stop is no error, and a new test may go.
	r.call(proto.MethodServerTestStop, nil, nil)
	r.call(proto.MethodServerTest, proto.ServerTestParams{IDs: []string{st.Servers[0].ID}}, nil)
	r.waitState("вторая проверка", func(st proto.State) bool {
		return st.Test == nil && r.journalHas(proto.LevelInfo, "отвечают 0 из 1")
	})
}

// Switching the VPN on ends a test on its own engine: the tunnel would take
// its connections. The VPN itself comes up as usual.
func TestServerTestGivesWayToTheVPN(t *testing.T) {
	sub := newSubServer(t, append([]string{link("DE", "de.example.com")}, slowLinks(40)...)...)
	r := startRig(t, emptyData(t), "ok")
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "a"}, nil); err != nil {
		t.Fatal(err)
	}
	r.call(proto.MethodServerTest, nil, nil)
	r.waitState("проверка идёт", func(st proto.State) bool { return st.Test != nil && st.Test.Stage == proto.TestTesting })
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	if st := r.state(); st.Test != nil {
		t.Errorf("проверка на своём движке пережила включение VPN: %+v", st.Test)
	}
	if !r.journalHas(proto.LevelInfo, "прервана: включается VPN") {
		t.Errorf("в журнале нет причины: %+v", r.journal)
	}

	// Through the VPN's engine now, and a stop of the VPN ends that one.
	r.call(proto.MethodServerTest, nil, nil)
	r.waitState("проверка через VPN", func(st proto.State) bool { return st.Test != nil && !st.Test.Own })
	r.call(proto.MethodVPNStop, nil, nil)
	r.waitPhase(proto.PhaseOff)
	if st := r.state(); st.Test != nil || st.Busy != nil {
		t.Errorf("проверка через VPN пережила его выключение: %+v", st.Test)
	}
	if !r.journalHas(proto.LevelInfo, "прервана: VPN выключается") {
		t.Errorf("в журнале нет причины: %+v", r.journal)
	}
}

// An own engine that does not start is said plainly and ends the test.
func TestServerTestOwnEngineThatDoesNotStart(t *testing.T) {
	r := startRig(t, newData(t), "crash")
	r.call(proto.MethodServerTest, nil, nil)
	r.waitState("конец неудачной проверки", func(st proto.State) bool {
		return st.Test == nil && st.Busy == nil && r.journalHas(proto.LevelProblem, "не запустился проверочный движок")
	})
	if st := r.state(); st.VPN.Phase != proto.PhaseOff || st.Servers[0].PingAt != nil {
		t.Errorf("неудачная проверка что-то поменяла: phase=%s pingAt=%v", st.VPN.Phase, st.Servers[0].PingAt)
	}
}
