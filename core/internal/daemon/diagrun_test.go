package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strelka/internal/ipc"
	"strelka/proto"
)

// offline stands in for the network the check reaches by itself: no test
// touches the internet. lookups counts the DNS questions.
type offline struct {
	fetchFails  atomic.Bool
	lookupFails atomic.Bool
	fetches     atomic.Int32
	lookups     atomic.Int32
}

func (o *offline) apply(opts *Options) {
	opts.Fetch = func(context.Context, string) error {
		o.fetches.Add(1)
		if o.fetchFails.Load() {
			return errors.New("no route to host")
		}
		return nil
	}
	opts.Lookup = func(context.Context, string) error {
		o.lookups.Add(1)
		if o.lookupFails.Load() {
			return errors.New("no such host")
		}
		return nil
	}
}

// diagListener is a second client that hears the check's steps as they go.
func (r *rig) diagListener() *ipc.Client {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := ipc.Dial(ctx, r.name)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { c.Close() })
	if err := c.Call(ctx, proto.MethodStateSubscribe, proto.TopicsParams{Topics: []string{proto.TopicDiag}}, nil); err != nil {
		r.t.Fatal(err)
	}
	return c
}

func heard(t *testing.T, c *ipc.Client, n int) []proto.DiagStep {
	t.Helper()
	var out []proto.DiagStep
	deadline := time.After(5 * time.Second)
	for len(out) < n {
		select {
		case note := <-c.Notifications():
			if note.Method != proto.TopicDiag {
				continue
			}
			var s proto.DiagStep
			if err := json.Unmarshal(note.Params, &s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		case <-deadline:
			t.Fatalf("услышал %d шагов из %d: %+v", len(out), n, out)
		}
	}
	return out
}

func runDiag(r *rig) []proto.DiagStep {
	r.t.Helper()
	var res proto.DiagRunResult
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.c.Call(ctx, proto.MethodDiagRun, nil, &res); err != nil {
		r.t.Fatalf("diag.run: %v", err)
	}
	return res.Steps
}

func summary(steps []proto.DiagStep) string {
	var b strings.Builder
	for _, s := range steps {
		b.WriteString(s.Step + "=" + s.Status + " ")
	}
	return strings.TrimSpace(b.String())
}

// Everything works: six steps, each said twice to a listener - when it
// starts and how it ended - and one line in the journal.
func TestDiagRunWalksTheWholeChain(t *testing.T) {
	var net offline
	r := startRigWith(t, newData(t), "ok", net.apply)
	quiet := r.diagListener()
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)

	steps := runDiag(r)
	if got := summary(steps); got != "network=ok internet=ok dns=ok engine=ok server=ok site=ok" {
		t.Fatalf("шаги: %s", got)
	}
	for _, s := range steps {
		if s.Text == "" || s.Advice != nil {
			t.Errorf("%s: текст %q, совет %v", s.Step, s.Text, s.Advice)
		}
	}
	if !strings.Contains(steps[4].Text, "Germany") {
		t.Errorf("сервер не назван: %q", steps[4].Text)
	}
	// With the engine up, the direct check goes through it, not by itself.
	if net.fetches.Load() != 0 || net.lookups.Load() != 1 {
		t.Errorf("напрямую: %d запросов службы, %d DNS", net.fetches.Load(), net.lookups.Load())
	}

	live := heard(t, quiet, 12)
	for i, s := range live {
		want := proto.DiagRunning
		if i%2 == 1 {
			want = steps[i/2].Status
		}
		if s.Status != want || s.Step != steps[i/2].Step {
			t.Errorf("уведомление %d: %s=%s, жду %s=%s", i, s.Step, s.Status, steps[i/2].Step, want)
		}
	}
	r.waitState("запись в журнале", func(proto.State) bool {
		return r.journalHas(proto.LevelInfo, "Проверка соединения: всё в порядке.")
	})
}

// VPN off: the line and the internet are checked by the daemon itself, the
// rest is skipped with advice to turn the VPN on.
func TestDiagRunWithTheVPNOff(t *testing.T) {
	var net offline
	r := startRigWith(t, newData(t), "ok", net.apply)
	steps := runDiag(r)
	if got := summary(steps); got != "network=ok internet=ok dns=ok engine=skip server=skip site=skip" {
		t.Fatalf("шаги: %s", got)
	}
	if steps[3].Advice == nil || !strings.Contains(*steps[3].Advice, "Включи VPN") {
		t.Errorf("совет: %+v", steps[3])
	}
	if net.fetches.Load() != 1 {
		t.Errorf("служба сама сходила напрямую %d раз, жду 1", net.fetches.Load())
	}
}

// Where the chain breaks, the check says so and stops asking further.
func TestDiagRunStopsWhereTheChainBreaks(t *testing.T) {
	var net offline
	r := startRigWith(t, newData(t), "ok", net.apply)

	r.network(false)
	r.waitState("нет сети", func(st proto.State) bool { return !st.Health.Network })
	steps := runDiag(r)
	if got := summary(steps); got != "network=fail internet=skip dns=skip engine=skip server=skip site=skip" {
		t.Fatalf("без сети: %s", got)
	}
	if steps[0].Advice == nil {
		t.Error("у провала нет совета")
	}
	r.network(true)
	r.waitState("сеть вернулась", func(st proto.State) bool { return st.Health.Network })

	net.fetchFails.Store(true)
	if got := summary(runDiag(r)); got != "network=ok internet=fail dns=skip engine=skip server=skip site=skip" {
		t.Fatalf("без интернета: %s", got)
	}
	net.fetchFails.Store(false)

	// A dead server: the site behind it is not tried.
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	r.answers("t-DE", "fail")
	steps = runDiag(r)
	if got := summary(steps); got != "network=ok internet=ok dns=ok engine=ok server=fail site=skip" {
		t.Fatalf("мёртвый сервер: %s", got)
	}
	if steps[4].Advice == nil || !strings.Contains(steps[4].Text, "Germany") {
		t.Errorf("сервер: %+v", steps[4])
	}
	r.waitState("итог в журнале", func(proto.State) bool {
		return r.journalHas(proto.LevelInfo, "Проверка соединения: сервер «Germany» не отвечает.")
	})

	// The server answers, the site behind it does not; DNS is down too,
	// which does not stop the check.
	r.answers("t-DE", "ok")
	r.answers("proxy", "fail")
	net.lookupFails.Store(true)
	if got := summary(runDiag(r)); got != "network=ok internet=ok dns=fail engine=ok server=ok site=fail" {
		t.Fatalf("сайт не открывается: %s", got)
	}
}

// One check at a time.
func TestDiagRunIsOneAtATime(t *testing.T) {
	var net offline
	slow := make(chan struct{})
	r := startRigWith(t, newData(t), "ok", func(o *Options) {
		net.apply(o)
		o.Fetch = func(ctx context.Context, _ string) error {
			select {
			case <-slow:
			case <-ctx.Done():
			}
			return nil
		}
	})
	steps := r.diagListener()
	done := make(chan []proto.DiagStep)
	go func() {
		var res proto.DiagRunResult
		r.c.Call(context.Background(), proto.MethodDiagRun, nil, &res)
		done <- res.Steps
	}()
	// Network said, internet under way: the first check hangs on the fetch.
	heard(t, steps, 3)
	// From another client: one connection's calls are answered in turn.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := steps.Call(ctx, proto.MethodDiagRun, nil, nil); codeOf(err) != proto.CodeBusy {
		t.Errorf("вторая проверка: %v", err)
	}
	close(slow)
	if steps := <-done; len(steps) != 6 {
		t.Errorf("первая проверка: %+v", steps)
	}
}

// legacy.detect finds nothing in a test's world but must answer; the real
// search is tested in internal/legacyfind.
func TestLegacyDetectAnswers(t *testing.T) {
	r := startRig(t, newData(t), "ok")
	var res proto.LegacyDetectResult
	r.call(proto.MethodLegacyDetect, nil, &res)
	// The daemon's own folders never count, even with a marker in them.
	os.WriteFile(filepath.Join(r.paths.Engines, "subscription.txt"), []byte("x"), 0o600)
	r.call(proto.MethodLegacyDetect, nil, &res)
	if res.Dir != nil && strings.EqualFold(*res.Dir, r.paths.Engines) {
		t.Errorf("нашёл свою папку: %s", *res.Dir)
	}
}
