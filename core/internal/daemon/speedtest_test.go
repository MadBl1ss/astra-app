package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"strelka/internal/engine/enginetest"
	"strelka/internal/speed"
	"strelka/proto"
)

// flow is a download that gives chunk bytes every gap until total, or
// forever when total is 0.
type flow struct {
	chunk, total int64
	gap          time.Duration
	given        int64
	once         sync.Once
	closed       chan struct{}
}

func newFlow(chunk int64, gap time.Duration, total int64) *flow {
	return &flow{chunk: chunk, gap: gap, total: total, closed: make(chan struct{})}
}

func (f *flow) Read(b []byte) (int, error) {
	if f.total > 0 && f.given >= f.total {
		return 0, io.EOF
	}
	select {
	case <-time.After(f.gap):
	case <-f.closed:
		return 0, errors.New("closed")
	}
	n := min(f.chunk, int64(len(b)))
	if f.total > 0 {
		n = min(n, f.total-f.given)
	}
	f.given += n
	return int(n), nil
}

func (f *flow) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

// quickSpeed is a speed test in a moment: a fast download, bounded at a
// third of a second, and a server called "nodl" whose download never starts.
func quickSpeed(o *Options) {
	o.SpeedLimits = speed.Limits{Bytes: 64 << 20, Time: 300 * time.Millisecond}
	o.SpeedOpen = func(ctx context.Context, server string) (io.ReadCloser, error) {
		if server == "a-nodl" {
			return nil, errors.New("connection refused")
		}
		return newFlow(32<<10, time.Millisecond, 0), nil
	}
}

func TestSpeedTestThroughTheVPN(t *testing.T) {
	sub := newSubServer(t, link("DE", "de.example.com"), link("dead", "gone.example.com"), link("nodl", "nodl.example.com"))
	r := startRigWith(t, emptyData(t), "ok", quickSpeed)
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "a"}, nil); err != nil {
		t.Fatal(err)
	}
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	r.call(proto.MethodStateSubscribe, proto.TopicsParams{Topics: []string{proto.TopicSpeed}}, nil)

	r.call(proto.MethodServerSpeed, proto.ServerSpeedParams{IDs: []string{"a-DE", "a-dead", "a-nodl"}}, nil)
	phases := map[string]bool{}
	var run *proto.SpeedRun
	r.waitState("конец замера", func(st proto.State) bool {
		if st.Speed != nil {
			run = st.Speed
			if st.Speed.Own || st.Busy == nil || *st.Busy != proto.BusyMeasuring {
				t.Fatalf("замер через VPN: own=%v busy=%v", st.Speed.Own, deref(st.Busy))
			}
			if st.Speed.Phase != nil {
				phases[*st.Speed.Current+"/"+*st.Speed.Phase] = true
			}
		}
		return st.Speed == nil && st.Busy == nil && r.journalHas(proto.LevelInfo, "Замер скорости: лучший")
	})
	if run == nil || run.MaxBytes != 64<<20 || run.MaxSeconds != 1 || len(run.IDs) != 3 {
		t.Errorf("замер в State: %+v", run)
	}
	// The fake answers pings faster than State goes out, so only the
	// download is sure to be seen; the pings are in the notifications.
	if !phases["a-DE/download"] {
		t.Errorf("не видно скачивания: %v", phases)
	}
	if phases["a-dead/download"] {
		t.Error("молчащий сервер всё равно скачивал")
	}

	got := map[string]proto.Server{}
	for _, s := range r.last.Servers {
		got[s.ID] = s
	}
	de := got["a-DE"]
	if de.Stats == nil || de.Stats.Speed == nil || *de.Stats.Speed <= 0 || de.Stats.SpeedAt == nil {
		t.Errorf("скорость a-DE не записана: %+v", de.Stats)
	}
	if de.Ping == nil || *de.Ping != enginetest.DelayOf("a-DE") {
		t.Errorf("пинг a-DE: %v", de.Ping)
	}
	if s := got["a-dead"]; s.Alive || (s.Stats != nil && s.Stats.Speed != nil) {
		t.Errorf("молчащий сервер: alive=%v stats=%+v", s.Alive, s.Stats)
	}
	if s := got["a-nodl"]; !s.Alive || (s.Stats != nil && s.Stats.Speed != nil) {
		t.Errorf("сервер без скачивания: alive=%v stats=%+v", s.Alive, s.Stats)
	}

	// What the window heard on the way: pings, then a needle that moves.
	var pings, downloads int
	var lastBytes int64
	for _, s := range r.speed {
		switch {
		case s.ID == "a-DE" && s.Phase == proto.SpeedPing && s.Ping != nil:
			pings++
		case s.ID == "a-DE" && s.Phase == proto.SpeedDownload:
			downloads++
			if s.Bytes < lastBytes || s.Mbps <= 0 {
				t.Errorf("отсчёт скачивания: %+v после %d байт", s, lastBytes)
			}
			lastBytes = s.Bytes
		case s.Phase == proto.SpeedDownload && s.ID != "a-DE":
			t.Errorf("отсчёт скачивания у %s", s.ID)
		}
	}
	if pings != speedPings || downloads < 2 {
		t.Errorf("отсчётов: пингов %d, скачивания %d", pings, downloads)
	}
	if !r.journalHas(proto.LevelInfo, "(a-DE), ") {
		t.Errorf("итог в журнале: %+v", r.journal)
	}
	if _, err := os.Stat(r.paths.StatsFile()); err != nil {
		t.Errorf("статистика не сохранена: %v", err)
	}
}

// With the VPN off the speed test brings its own engine, gate included,
// and leaves the VPN off.
func TestSpeedTestWithTheVPNOff(t *testing.T) {
	sub := newSubServer(t, link("DE", "de.example.com"))
	paths := emptyData(t)
	r := startRigWith(t, paths, "ok", quickSpeed)
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "a"}, nil); err != nil {
		t.Fatal(err)
	}
	// Empty: the selected server.
	r.call(proto.MethodServerSpeed, nil, nil)
	saw := map[string]bool{}
	r.waitState("конец замера на своём движке", func(st proto.State) bool {
		if st.VPN.Phase != proto.PhaseOff {
			t.Fatalf("замер включил VPN: %s", st.VPN.Phase)
		}
		if st.Speed != nil {
			saw[st.Speed.Stage] = true
			saw["own"] = saw["own"] || st.Speed.Own
		}
		return st.Speed == nil && st.Busy == nil && r.journalHas(proto.LevelInfo, "Замер скорости: лучший")
	})
	if !saw["own"] || !saw[proto.TestPreparing] {
		t.Errorf("ход замера: %v", saw)
	}
	if s := r.last.Servers[0]; s.Stats == nil || s.Stats.Speed == nil {
		t.Errorf("скорость не записана: %+v", s.Stats)
	}
	r.eventually("проверочный движок убрал за собой конфиг", func() bool {
		_, err := os.Stat(scratch(paths.RunConfig(), "probe"))
		return os.IsNotExist(err)
	})
}

// A stop in the middle of a download ends the run at once and keeps what
// was measured before.
func TestSpeedTestStops(t *testing.T) {
	sub := newSubServer(t, link("DE", "de.example.com"), link("NL", "nl.example.com"))
	r := startRigWith(t, emptyData(t), "ok", func(o *Options) {
		o.SpeedLimits = speed.Limits{Bytes: 1 << 40, Time: time.Minute}
		o.SpeedOpen = func(context.Context, string) (io.ReadCloser, error) {
			return newFlow(1<<10, 5*time.Millisecond, 0), nil
		}
	})
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "a"}, nil); err != nil {
		t.Fatal(err)
	}
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	r.call(proto.MethodServerSpeed, proto.ServerSpeedParams{IDs: []string{"a-DE", "a-NL"}}, nil)
	r.waitState("скачивание идёт", func(st proto.State) bool {
		return st.Speed != nil && st.Speed.Phase != nil && *st.Speed.Phase == proto.SpeedDownload
	})
	// Another job waits for this one.
	if err := r.tryCall(proto.MethodServerTest, nil, nil); err == nil {
		t.Error("проверка серверов запустилась посреди замера скорости")
	}
	started := time.Now()
	r.call(proto.MethodServerTestStop, nil, nil)
	st := r.state()
	if st.Speed != nil || st.Busy != nil {
		t.Fatalf("замер не остановился: %+v busy=%v", st.Speed, deref(st.Busy))
	}
	r.waitState("журнал", func(proto.State) bool {
		return r.journalHas(proto.LevelInfo, "Замер скорости остановлен")
	})
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("остановка заняла %v", took)
	}
	for _, s := range st.Servers {
		if s.Stats != nil && s.Stats.Speed != nil {
			t.Errorf("%s: скорость записана по прерванному скачиванию", s.ID)
		}
	}
	// The VPN's own engine is in no hurry to stop for it.
	if st.VPN.Phase != proto.PhaseOn {
		t.Errorf("VPN после остановки замера: %s", st.VPN.Phase)
	}
}

func TestSpeedTestRefusals(t *testing.T) {
	var links []string
	for i := range maxSpeedServers + 1 {
		links = append(links, link(fmt.Sprintf("S%02d", i), fmt.Sprintf("s%02d.example.com", i)))
	}
	sub := newSubServer(t, links...)
	r := startRigWith(t, emptyData(t), "ok", quickSpeed)
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "a"}, nil); err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, s := range r.state().Servers {
		all = append(all, s.ID)
	}
	check := func(params any, code int) {
		t.Helper()
		err := r.tryCall(proto.MethodServerSpeed, params, nil)
		var pe *proto.Error
		if !errors.As(err, &pe) || pe.Code != code {
			t.Errorf("%v: ошибка %v, жду код %d", params, err, code)
		}
	}
	check(proto.ServerSpeedParams{IDs: all}, proto.CodeInvalid)
	check(proto.ServerSpeedParams{IDs: []string{"нет-такого"}}, proto.CodeNotFound)
	if st := r.state(); st.Speed != nil || st.Busy != nil {
		t.Errorf("отказ оставил следы: %+v", st.Speed)
	}
}
