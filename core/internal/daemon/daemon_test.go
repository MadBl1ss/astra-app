package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strelka/internal/engine"
	"strelka/internal/engine/enginetest"
	"strelka/internal/ipc"
	"strelka/internal/store"
	"strelka/proto"
)

func TestMain(m *testing.M) {
	enginetest.Main()
	os.Exit(m.Run())
}

var pipeSeq atomic.Int64

// rig is a daemon over the fake engine, served on its own channel, plus a
// client that is subscribed to state and journal.
type rig struct {
	t      *testing.T
	paths  store.Paths
	name   string
	cancel context.CancelFunc
	exited chan error
	c      *ipc.Client

	last    proto.State
	journal []proto.JournalEntry
}

// fastPolicy has the shape of the real one with pauses a test can wait out.
var fastPolicy = engine.Policy{
	Delays:      []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 150 * time.Millisecond, 200 * time.Millisecond},
	StableAfter: time.Minute,
	CrashLimit:  5,
	CrashWindow: 2 * time.Minute,
}

// newData makes a data folder with a minimal ready config, as stage 01
// expects to find one.
func newData(t *testing.T) store.Paths {
	t.Helper()
	paths := store.Paths{Data: t.TempDir(), Engines: t.TempDir()}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	cfg := `{"log":{"level":"info","output":"box.log"},"inbounds":[{"type":"mixed","listen_port":20808}]}`
	if err := os.WriteFile(paths.RunConfig(), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return paths
}

func startRig(t *testing.T, paths store.Paths, script string) *rig {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{
		t:      t,
		paths:  paths,
		name:   fmt.Sprintf("strelka-test-daemon-%d-%d", os.Getpid(), pipeSeq.Add(1)),
		exited: make(chan error, 1),
	}
	l, err := ipc.Listen(r.name)
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(Options{
		Paths: paths,
		Launch: func(cfgPath string) engine.Launch {
			return engine.Launch{
				Exe:  self,
				Args: []string{"run", "-c", cfgPath},
				Env:  enginetest.Env(script, paths.Data, "", ""),
			}
		},
		Policy:    fastPolicy,
		StopGrace: time.Second,
		Logf:      t.Logf,
	})
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go func() { r.exited <- d.Run(ctx, l) }()
	t.Cleanup(r.shutdown)

	dialCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	r.c, err = ipc.Dial(dialCtx, r.name)
	if err != nil {
		t.Fatalf("не подключился к службе: %v", err)
	}
	r.call(proto.MethodStateSubscribe, proto.TopicsParams{Topics: []string{proto.TopicState, proto.TopicJournal}}, nil)
	// Subscribing to state brings the current document at once.
	r.waitState("первое состояние после подписки", func(proto.State) bool { return true })
	return r
}

// shutdown stops the daemon the way Ctrl+C does and waits for it.
func (r *rig) shutdown() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	r.cancel = nil
	select {
	case err := <-r.exited:
		if err != nil {
			r.t.Errorf("служба завершилась с ошибкой: %v", err)
		}
	case <-time.After(20 * time.Second):
		r.t.Error("служба не завершилась")
	}
	r.c.Close()
}

func (r *rig) call(method string, params, result any) {
	r.t.Helper()
	if err := r.tryCall(method, params, result); err != nil {
		r.t.Fatalf("%s: %v", method, err)
	}
}

func (r *rig) tryCall(method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return r.c.Call(ctx, method, params, result)
}

// waitState reads notifications until a State satisfies ok. It returns the
// phases seen on the way, in order, without repeats.
func (r *rig) waitState(what string, ok func(proto.State) bool) []string {
	r.t.Helper()
	var phases []string
	deadline := time.After(20 * time.Second)
	for {
		select {
		case n, open := <-r.c.Notifications():
			if !open {
				r.t.Fatalf("соединение закрылось, пока ждал: %s", what)
			}
			switch n.Method {
			case proto.TopicJournal:
				var e proto.JournalEntry
				if err := json.Unmarshal(n.Params, &e); err != nil {
					r.t.Fatalf("запись журнала не разобрана: %v", err)
				}
				r.journal = append(r.journal, e)
			case proto.TopicState:
				var st proto.State
				if err := json.Unmarshal(n.Params, &st); err != nil {
					r.t.Fatalf("State не разобран: %v", err)
				}
				if st.Rev < r.last.Rev {
					r.t.Fatalf("rev пошёл назад: %d после %d", st.Rev, r.last.Rev)
				}
				r.last = st
				if len(phases) == 0 || phases[len(phases)-1] != st.VPN.Phase {
					phases = append(phases, st.VPN.Phase)
				}
				if ok(st) {
					return phases
				}
			}
		case <-deadline:
			r.t.Fatalf("не дождался: %s. Последнее состояние: phase=%s busy=%v, фазы по пути: %v",
				what, r.last.VPN.Phase, deref(r.last.Busy), phases)
		}
	}
}

func (r *rig) waitPhase(phase string) []string {
	r.t.Helper()
	return r.waitState("phase = "+phase, func(st proto.State) bool { return st.VPN.Phase == phase })
}

func (r *rig) engine() proto.EngineStatus {
	r.t.Helper()
	var res proto.EngineStatusResult
	r.call(proto.MethodEngineStatus, nil, &res)
	if len(res.Engines) != 1 {
		r.t.Fatalf("engine.status: %+v", res)
	}
	return res.Engines[0]
}

func (r *rig) journalHas(level, part string) bool {
	for _, e := range r.journal {
		if e.Level == level && strings.Contains(e.Text, part) {
			return true
		}
	}
	return false
}

func deref(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); engine.Alive(pid); {
		if time.Now().After(deadline) {
			t.Fatalf("процесс движка %d всё ещё жив", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func killProcess(t *testing.T, pid int) {
	t.Helper()
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Kill(); err != nil {
		t.Fatalf("не удалось завершить процесс %d: %v", pid, err)
	}
	p.Release()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestStartCrashRecoverStop(t *testing.T) {
	r := startRig(t, newData(t), "ok")

	var hello proto.HelloResult
	r.call(proto.MethodHello, proto.HelloParams{Client: "ctl", Version: "test", Proto: proto.Version}, &hello)
	if hello.Proto != proto.Version || hello.Version != Version || hello.OS == "" {
		t.Errorf("hello: %+v", hello)
	}
	if r.last.VPN.Phase != proto.PhaseOff || r.last.VPN.Desired != proto.DesiredOff || r.last.Busy != nil {
		t.Fatalf("новая служба: phase=%s desired=%s busy=%s", r.last.VPN.Phase, r.last.VPN.Desired, deref(r.last.Busy))
	}

	// off -> starting -> on
	r.call(proto.MethodVPNStart, nil, nil)
	phases := r.waitPhase(proto.PhaseOn)
	if !contains(phases, proto.PhaseStarting) {
		t.Errorf("по пути к on не было starting: %v", phases)
	}
	st := r.last
	if st.VPN.Desired != proto.DesiredOn || st.VPN.Since == nil || st.Busy != nil || st.VPN.FailReason != nil {
		t.Errorf("после старта: %+v busy=%s", st.VPN, deref(st.Busy))
	}
	first := r.engine()
	if !first.Running || first.PID == 0 || first.Since == nil || !engine.Alive(first.PID) {
		t.Fatalf("engine.status после старта: %+v", first)
	}

	// The engine got this run's API address and secret, and logs to stderr.
	var cfg struct {
		Log          map[string]any `json:"log"`
		Inbounds     []any          `json:"inbounds"`
		Experimental struct {
			ClashAPI struct {
				Controller string `json:"external_controller"`
				Secret     string `json:"secret"`
			} `json:"clash_api"`
		} `json:"experimental"`
	}
	raw, _ := os.ReadFile(r.paths.RunConfig())
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.Experimental.ClashAPI.Controller, "127.0.0.1:") || len(cfg.Experimental.ClashAPI.Secret) != 32 {
		t.Errorf("clash_api в конфиге: %+v", cfg.Experimental.ClashAPI)
	}
	if _, has := cfg.Log["output"]; has || cfg.Log["level"] != "info" || len(cfg.Inbounds) != 1 {
		t.Errorf("конфиг после подстановки: log=%v inbounds=%v", cfg.Log, cfg.Inbounds)
	}

	// on -> recovering -> on, without anyone asking
	killed := time.Now()
	killProcess(t, first.PID)
	r.waitPhase(proto.PhaseRecovering)
	if r.last.VPN.Desired != proto.DesiredOn || r.last.VPN.Since != nil || r.last.Health.Verdict != proto.VerdictEngineRecovering {
		t.Errorf("во время восстановления: %+v verdict=%s", r.last.VPN, r.last.Health.Verdict)
	}
	r.waitPhase(proto.PhaseOn)
	if took := time.Since(killed); took > 5*time.Second {
		t.Errorf("восстановление заняло %v", took)
	}
	second := r.engine()
	if !second.Running || second.PID == first.PID || second.Restarts != 1 {
		t.Errorf("engine.status после восстановления: %+v (был pid %d)", second, first.PID)
	}
	if !r.journalHas(proto.LevelProblem, "неожиданно остановился") || !r.journalHas(proto.LevelInfo, "VPN снова работает") {
		t.Errorf("в журнале нет падения и восстановления: %+v", r.journal)
	}

	// restart: a new process, and the journal says why
	r.call(proto.MethodVPNRestart, nil, nil)
	r.waitPhase(proto.PhaseStarting)
	r.waitPhase(proto.PhaseOn)
	third := r.engine()
	if third.PID == second.PID || third.Restarts != 0 {
		t.Errorf("после vpn.restart: %+v (был pid %d)", third, second.PID)
	}
	waitGone(t, second.PID)

	// on -> stopping -> off
	// (A healthy engine stops within one broadcast interval, so "stopping"
	// may be merged away here; TestStopWithAHungEngine is where it shows.)
	r.call(proto.MethodVPNStop, nil, nil)
	r.waitPhase(proto.PhaseOff)
	if r.last.VPN.Desired != proto.DesiredOff || r.last.Busy != nil || r.last.VPN.Since != nil {
		t.Errorf("после остановки: %+v busy=%s", r.last.VPN, deref(r.last.Busy))
	}
	waitGone(t, third.PID)
	if es := r.engine(); es.Running || es.PID != 0 || es.Since != nil {
		t.Errorf("engine.status после остановки: %+v", es)
	}

	// The journal on disk has the same story, one JSON object per line.
	f, err := os.Open(r.paths.Journal())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var texts []string
	var lastID int64
	for sc := bufio.NewScanner(f); sc.Scan(); {
		var e proto.JournalEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("строка журнала не разобрана: %v", err)
		}
		if e.ID <= lastID {
			t.Errorf("id журнала не растёт: %d после %d", e.ID, lastID)
		}
		lastID = e.ID
		texts = append(texts, e.Text)
	}
	all := strings.Join(texts, "\n")
	for _, part := range []string{"Служба запущена", "VPN включён", "неожиданно остановился", "Перезапускаю VPN", "VPN выключен"} {
		if !strings.Contains(all, part) {
			t.Errorf("в journal.jsonl нет записи «%s»:\n%s", part, all)
		}
	}
}

func TestDesiredSurvivesDaemonRestart(t *testing.T) {
	paths := newData(t)

	r := startRig(t, paths, "ok")
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	pid := r.engine().PID
	r.shutdown()
	// Shutting the daemon down stops the engine but is not the user's "off".
	waitGone(t, pid)

	r = startRig(t, paths, "ok")
	if r.last.VPN.Desired != proto.DesiredOn {
		t.Fatalf("после перезапуска службы desired = %s, жду on", r.last.VPN.Desired)
	}
	r.waitPhase(proto.PhaseOn) // nobody called vpn.start
	r.call(proto.MethodVPNStop, nil, nil)
	r.waitPhase(proto.PhaseOff)
	// Journal ids continue across the restart.
	firstID := r.journal[0].ID
	if firstID <= 2 {
		t.Errorf("после перезапуска журнал начал нумерацию заново: id %d", firstID)
	}
	r.shutdown()

	r = startRig(t, paths, "ok")
	time.Sleep(300 * time.Millisecond)
	var st proto.State
	r.call(proto.MethodStateGet, nil, &st)
	if st.VPN.Desired != proto.DesiredOff || st.VPN.Phase != proto.PhaseOff {
		t.Errorf("выключенный VPN включился после перезапуска службы: %+v", st.VPN)
	}
	if es := r.engine(); es.Running {
		t.Errorf("движок запущен при desired = off: %+v", es)
	}
}

func TestCrashLoopEndsInFailedAndCanBeRetried(t *testing.T) {
	// Five crashes, then healthy: the first start fails, the retry works.
	r := startRig(t, newData(t), "crash,crash,crash,crash,crash,ok")
	r.call(proto.MethodVPNStart, nil, nil)
	phases := r.waitPhase(proto.PhaseFailed)
	if contains(phases, proto.PhaseOn) || contains(phases, proto.PhaseRecovering) {
		t.Errorf("движок ни разу не поднялся, а фазы: %v", phases)
	}
	st := r.last
	if st.VPN.FailReason == nil || *st.VPN.FailReason != proto.FailCrashLoop || st.VPN.FailText == nil || *st.VPN.FailText == "" {
		t.Fatalf("причина сбоя: %s / %s", deref(st.VPN.FailReason), deref(st.VPN.FailText))
	}
	if st.VPN.Desired != proto.DesiredOn || st.Busy != nil || st.Health.Verdict != proto.VerdictEngineFailed {
		t.Errorf("в failed: desired=%s busy=%s verdict=%s", st.VPN.Desired, deref(st.Busy), st.Health.Verdict)
	}
	if !r.journalHas(proto.LevelCritical, "VPN не запускается") {
		t.Errorf("в журнале нет критичной записи: %+v", r.journal)
	}
	if n := enginetest.Starts(r.paths.Data); n != 5 {
		t.Errorf("до crashloop движок запускался %d раз, жду 5", n)
	}

	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	if r.last.VPN.FailReason != nil || r.last.VPN.FailText != nil {
		t.Errorf("причина сбоя не снята после успешного старта: %s", deref(r.last.VPN.FailReason))
	}
}

func TestStopFromFailedAndMissingConfig(t *testing.T) {
	paths := newData(t)
	if err := os.Remove(paths.RunConfig()); err != nil {
		t.Fatal(err)
	}
	r := startRig(t, paths, "ok")
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseFailed)
	if r.last.VPN.FailReason == nil || *r.last.VPN.FailReason != proto.FailMissing {
		t.Fatalf("нет конфига: причина %s, жду missing", deref(r.last.VPN.FailReason))
	}
	if n := enginetest.Starts(paths.Data); n != 0 {
		t.Errorf("без конфига движок всё равно запускался: %d", n)
	}

	r.call(proto.MethodVPNStop, nil, nil)
	r.waitPhase(proto.PhaseOff)
	if r.last.VPN.FailReason != nil || r.last.VPN.Desired != proto.DesiredOff {
		t.Errorf("после stop из failed: %+v", r.last.VPN)
	}

	// A config that is not JSON is a different reason.
	os.WriteFile(paths.RunConfig(), []byte("{ это не JSON"), 0o600)
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseFailed)
	if r.last.VPN.FailReason == nil || *r.last.VPN.FailReason != proto.FailConfig {
		t.Errorf("битый конфиг: причина %s, жду config", deref(r.last.VPN.FailReason))
	}
}

func TestErrorsAreCodedAndWorded(t *testing.T) {
	r := startRig(t, newData(t), "ok")
	check := func(method string, params any, code int) {
		t.Helper()
		err := r.tryCall(method, params, nil)
		var perr *proto.Error
		if !errors.As(err, &perr) || perr.Code != code {
			t.Errorf("%s: %v, жду код %d", method, err, code)
			return
		}
		if perr.Message == "" || !strings.ContainsAny(perr.Message, "абвгдеёжзийклмнопрстуфхцчшщъыьэюя") {
			t.Errorf("%s: текст ошибки должен быть русским: %q", method, perr.Message)
		}
	}
	check("mode.set", map[string]string{"mode": "global"}, proto.CodeMethodNotFound) // a later stage
	check("нет.такого", nil, proto.CodeMethodNotFound)
	check(proto.MethodStateSubscribe, map[string]any{"topics": []string{"всё"}}, proto.CodeInvalidParams)
	check(proto.MethodStateSubscribe, map[string]any{"topics": "state"}, proto.CodeInvalidParams)
	check(proto.MethodVPNRestart, nil, proto.CodeInvalid) // VPN is off

	// start, stop and stop again are all fine and idempotent
	r.call(proto.MethodVPNStop, nil, nil)
	r.call(proto.MethodVPNStart, nil, nil)
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	r.call(proto.MethodVPNStart, nil, nil)
}

// A hung engine must not hang "stop": the user gets off within the grace.
func TestStopWithAHungEngine(t *testing.T) {
	r := startRig(t, newData(t), "hang")
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	pid := r.engine().PID

	asked := time.Now()
	r.call(proto.MethodVPNStop, nil, nil)
	// While it is stopping, a start is refused rather than queued.
	err := r.tryCall(proto.MethodVPNStart, nil, nil)
	var perr *proto.Error
	if !errors.As(err, &perr) || perr.Code != proto.CodeBusy {
		t.Errorf("vpn.start во время остановки: %v, жду busy", err)
	}
	sawStopping := false
	r.waitState("phase = off", func(st proto.State) bool {
		if st.VPN.Phase == proto.PhaseStopping {
			sawStopping = st.Busy != nil && *st.Busy == proto.BusyStopping && st.VPN.Desired == proto.DesiredOff
		}
		return st.VPN.Phase == proto.PhaseOff
	})
	if !sawStopping {
		t.Error("пока движок останавливался, не было состояния stopping с busy = stopping")
	}
	if took := time.Since(asked); took > 5*time.Second {
		t.Errorf("остановка зависшего движка заняла %v", took)
	}
	waitGone(t, pid)
}

// A burst of changes leaves as a few documents, not one per change, and the
// last one carries the final state.
func TestStateBroadcastIsCoalesced(t *testing.T) {
	r := startRig(t, newData(t), "crash")
	start := time.Now()
	startRev := r.last.Rev
	r.call(proto.MethodVPNStart, nil, nil)
	docs := 0
	r.waitState("failed после серии падений", func(st proto.State) bool {
		docs++
		return st.VPN.Phase == proto.PhaseFailed
	})
	elapsed := time.Since(start)
	changes := int(r.last.Rev - startRev)
	limit := int(elapsed/minStateInterval) + 2
	if docs > limit {
		t.Errorf("за %v пришло %d документов, предел %d (10 в секунду)", elapsed, docs, limit)
	}
	if docs >= changes {
		t.Errorf("изменений %d, документов %d: склейки не было", changes, docs)
	}
	var st proto.State
	r.call(proto.MethodStateGet, nil, &st)
	if st.Rev != r.last.Rev || st.VPN.Phase != proto.PhaseFailed {
		t.Errorf("последний документ (rev %d) не совпадает с текущим состоянием (rev %d)", r.last.Rev, st.Rev)
	}
}

func TestSecondDaemonOnTheSameChannelIsRefused(t *testing.T) {
	r := startRig(t, newData(t), "ok")
	if l, err := ipc.Listen(r.name); err == nil {
		l.Close()
		t.Fatal("второй слушатель на канале службы должен получить ошибку")
	}
}

func TestPatchConfig(t *testing.T) {
	in := `{
	  "log": {"level": "warn", "output": "box.log", "timestamp": true},
	  "inbounds": [{"type": "mixed", "listen": "127.0.0.1", "listen_port": 20808}],
	  "outbounds": [{"type": "direct", "tag": "direct"}],
	  "experimental": {"clash_api": {"external_controller": "127.0.0.1:9090", "secret": "old", "default_mode": "Rule"},
	                   "cache_file": {"enabled": true}},
	  "big": 12345678901234567890
	}`
	out, err := patchConfig([]byte(in), "127.0.0.1:41234", "newsecret")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	api := got["experimental"].(map[string]any)["clash_api"].(map[string]any)
	if api["external_controller"] != "127.0.0.1:41234" || api["secret"] != "newsecret" || api["default_mode"] != "Rule" {
		t.Errorf("clash_api: %v", api)
	}
	if got["experimental"].(map[string]any)["cache_file"] == nil {
		t.Error("соседние поля experimental потеряны")
	}
	log := got["log"].(map[string]any)
	if _, has := log["output"]; has || log["level"] != "warn" || log["timestamp"] != true {
		t.Errorf("log: %v", log)
	}
	if len(got["inbounds"].([]any)) != 1 || len(got["outbounds"].([]any)) != 1 {
		t.Error("inbounds или outbounds изменились")
	}
	if !strings.Contains(string(out), "12345678901234567890") {
		t.Error("большое число испорчено при пересборке")
	}

	// An empty config still gets what the daemon needs.
	out, err = patchConfig([]byte(`{}`), "127.0.0.1:1", "s")
	if err != nil || !strings.Contains(string(out), `"external_controller": "127.0.0.1:1"`) {
		t.Errorf("пустой конфиг: %s, %v", out, err)
	}
	for _, bad := range []string{``, `[]`, `null`, `{"experimental": 5}`, `{"log": []}`, `{"experimental": {"clash_api": "x"}}`} {
		if _, err := patchConfig([]byte(bad), "a", "b"); err == nil {
			t.Errorf("конфиг %q должен дать ошибку", bad)
		}
	}
}

func TestJournalSurvivesATornLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j := openJournal(path)
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	j.add(now, proto.CatSystem, proto.LevelInfo, "первая")
	j.add(now, proto.CatVPN, proto.LevelProblem, "вторая")
	j.close()

	// Power went out in the middle of a write.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"id":3,"t":"2026-11-12T10:00:0`)
	f.Close()

	j = openJournal(path)
	defer j.close()
	if len(j.entries) != 2 || j.entries[1].Text != "вторая" {
		t.Fatalf("прочитано %d записей: %+v", len(j.entries), j.entries)
	}
	if e := j.add(now, proto.CatVPN, proto.LevelInfo, "третья"); e.ID != 3 || e.Incident != nil {
		t.Errorf("новая запись: %+v, жду id 3", e)
	}

	// The ring is bounded; the file keeps everything.
	for i := 0; i < journalRing+10; i++ {
		j.add(now, proto.CatVPN, proto.LevelInfo, "запись")
	}
	if len(j.entries) != journalRing {
		t.Errorf("в кольце %d записей, предел %d", len(j.entries), journalRing)
	}
}
