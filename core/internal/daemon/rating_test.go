package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"strelka/internal/engine/enginetest"
	"strelka/proto"
)

// While the VPN works the rating learns about the servers nobody uses,
// without a test and without a word in the journal; switched off, it stops.
func TestQuietRoundsKeepTheRatingFresh(t *testing.T) {
	sub := newSubServer(t, link("DE", "de.example.com"), link("NL", "nl.example.com"), link("dead", "gone.example.com"))
	r := startRigWith(t, emptyData(t), "ok", func(o *Options) {
		o.RateEvery, o.RateSettle = 300*time.Millisecond, 100*time.Millisecond
		o.HealthTick = 50 * time.Millisecond
	})
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "a"}, nil); err != nil {
		t.Fatal(err)
	}
	// Nothing happens while the VPN is off.
	time.Sleep(400 * time.Millisecond)
	for _, s := range r.state().Servers {
		if s.PingAt != nil {
			t.Fatalf("тихая проверка при выключенном VPN: %s", s.ID)
		}
	}
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	r.waitState("тихие проверки", func(st proto.State) bool {
		n := 0
		for _, s := range st.Servers {
			if s.ID != "a-DE" && s.Stats != nil && s.Stats.Samples+int(s.Stats.Loss*100) > 0 && s.PingAt != nil {
				n++
			}
		}
		return n == 2
	})
	got := map[string]proto.Server{}
	for _, s := range r.last.Servers {
		got[s.ID] = s
	}
	if nl := got["a-NL"]; !nl.Alive || nl.Ping == nil || *nl.Ping != enginetest.DelayOf("a-NL") {
		t.Errorf("a-NL после тихой проверки: %+v", nl)
	}
	if dead := got["a-dead"]; dead.Alive || dead.Stats == nil || dead.Stats.Loss <= 0 {
		t.Errorf("a-dead после тихой проверки: %+v stats=%+v", dead, dead.Stats)
	}
	for _, e := range r.journal {
		if e.Cat == proto.CatServer {
			t.Errorf("тихая проверка написала в журнал: %s", e.Text)
		}
	}

	// Rounds go on, one after another.
	first := *got["a-NL"].PingAt
	r.waitState("следующий тихий круг", func(st proto.State) bool {
		for _, s := range st.Servers {
			if s.ID == "a-NL" && s.PingAt != nil && s.PingAt.After(first) {
				return true
			}
		}
		return false
	})

	// Off: no more of them.
	patch, _ := json.Marshal(map[string]bool{"rating": false})
	r.call(proto.MethodSettingsSet, proto.SettingsSetParams{Patch: patch}, nil)
	if r.state().Settings.Rating {
		t.Fatal("настройка не выключилась")
	}
	time.Sleep(200 * time.Millisecond) // a round under way may still land
	var before *time.Time
	for _, s := range r.state().Servers {
		if s.ID == "a-NL" {
			before = s.PingAt
		}
	}
	time.Sleep(800 * time.Millisecond)
	for _, s := range r.state().Servers {
		if s.ID == "a-NL" && !s.PingAt.Equal(*before) {
			t.Errorf("тихие проверки идут при выключенной настройке: %v -> %v", before, s.PingAt)
		}
	}
}
