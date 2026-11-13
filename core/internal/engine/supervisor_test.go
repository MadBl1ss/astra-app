package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"strelka/internal/clash"
	"strelka/internal/engine/enginetest"
)

const envParentHelper = "STRELKA_TEST_PARENT"

func TestMain(m *testing.M) {
	enginetest.Main()
	if dir := os.Getenv(envParentHelper); dir != "" {
		parentHelper(dir)
	}
	os.Exit(m.Run())
}

// harness runs a supervisor over the fake engine and collects its events.
type harness struct {
	t      *testing.T
	dir    string
	sup    *Supervisor
	events chan Event

	mu     sync.Mutex
	api    string
	secret string
}

func fakeConfig(script, dir string, onAPI func(api, secret string)) Config {
	self, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return Config{
		Name: "fake",
		Prepare: func() (Launch, func(context.Context) error, error) {
			port, err := FreePort()
			if err != nil {
				return Launch{}, nil, err
			}
			secret, err := NewSecret()
			if err != nil {
				return Launch{}, nil, err
			}
			api := fmt.Sprintf("127.0.0.1:%d", port)
			if onAPI != nil {
				onAPI(api, secret)
			}
			cl := clash.New(api, secret)
			ready := func(ctx context.Context) error {
				_, err := cl.Version(ctx)
				return err
			}
			return Launch{Exe: self, Env: enginetest.Env(script, dir, api, secret)}, ready, nil
		},
		// Short pauses keep the tests quick; the real numbers are checked
		// against the pure policy in policy_test.go.
		Policy: Policy{
			Delays:      []time.Duration{40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond, 320 * time.Millisecond},
			StableAfter: time.Minute,
			CrashLimit:  5,
			CrashWindow: 2 * time.Minute,
		},
		ReadyPoll: 20 * time.Millisecond,
	}
}

func newHarness(t *testing.T, script string, tweak func(*Config)) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir(), events: make(chan Event, 256)}
	cfg := fakeConfig(script, h.dir, func(api, secret string) {
		h.mu.Lock()
		h.api, h.secret = api, secret
		h.mu.Unlock()
	})
	cfg.LogFile = filepath.Join(h.dir, "fake.log")
	cfg.OnEvent = func(ev Event) { h.events <- ev }
	if tweak != nil {
		tweak(&cfg)
	}
	h.sup = New(cfg)
	t.Cleanup(h.sup.Close)
	return h
}

// expect takes the next event and fails unless it is of the wanted kind.
func (h *harness) expect(kind EventKind) Event {
	h.t.Helper()
	select {
	case ev := <-h.events:
		if ev.Kind != kind {
			h.t.Fatalf("событие %v (%+v), жду %v\nвывод движка:\n%s", ev.Kind, ev, kind, strings.Join(h.sup.Tail(20), "\n"))
		}
		return ev
	case <-time.After(15 * time.Second):
		h.t.Fatalf("не дождался события %v\nвывод движка:\n%s", kind, strings.Join(h.sup.Tail(20), "\n"))
		return Event{}
	}
}

func (h *harness) control(path string) {
	h.t.Helper()
	h.mu.Lock()
	api := h.api
	h.mu.Unlock()
	// The engine dies while answering /fake/crash, so an error is expected.
	resp, err := http.Get("http://" + api + path)
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for Alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("процесс %d всё ещё жив", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartReadyStop(t *testing.T) {
	h := newHarness(t, "ok", nil)
	h.sup.Start()
	started := h.expect(Started)
	ready := h.expect(Ready)
	if started.PID == 0 || ready.PID != started.PID {
		t.Fatalf("pid: started %d, ready %d", started.PID, ready.PID)
	}
	st := h.sup.Status()
	if !st.Running || st.PID != started.PID || st.Since.IsZero() || st.Restarts != 0 {
		t.Errorf("status: %+v", st)
	}
	if !Alive(started.PID) {
		t.Fatal("движок готов, но процесса нет")
	}

	// A second Start changes nothing.
	h.sup.Start()

	h.sup.Stop()
	h.expect(Stopped)
	waitGone(t, started.PID)
	if st := h.sup.Status(); st.Running || st.PID != 0 {
		t.Errorf("status после остановки: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "graceful")); err == nil {
		t.Log("движок остановлен мягко (CTRL_BREAK / SIGTERM)")
	} else {
		t.Log("мягкая остановка недоступна в этом окружении (нет общей консоли): движок снят принудительно")
	}

	// Stop of a stopped engine is still confirmed.
	h.sup.Stop()
	h.expect(Stopped)

	log, err := os.ReadFile(filepath.Join(h.dir, "fake.log"))
	if err != nil || !strings.Contains(string(log), "fake engine start=1 mode=ok") {
		t.Errorf("вывод движка не попал в файл: %q, %v", log, err)
	}
	if tail := h.sup.Tail(50); len(tail) == 0 || !strings.Contains(tail[0], "fake engine start=1") {
		t.Errorf("вывод движка не попал в кольцо: %v", tail)
	}
}

func TestCrashIsNoticedAtOnceAndRestarted(t *testing.T) {
	h := newHarness(t, "ok", nil)
	h.sup.Start()
	first := h.expect(Started)
	h.expect(Ready)

	asked := time.Now()
	h.control("/fake/crash")
	ex := h.expect(Exited)
	if noticed := ex.At.Sub(asked); noticed > 2*time.Second {
		t.Errorf("падение замечено через %v: это опрос, а не ожидание процесса", noticed)
	}
	if ex.ExitCode != 3 || ex.NotReady || ex.PID != first.PID || ex.Crashes != 1 {
		t.Errorf("событие о падении: %+v", ex)
	}
	if ex.RetryIn != 40*time.Millisecond {
		t.Errorf("первая пауза %v, жду 40 мс", ex.RetryIn)
	}
	if !strings.Contains(strings.Join(ex.Tail, "\n"), "crashing on request") {
		t.Errorf("в событии нет последних слов движка: %v", ex.Tail)
	}

	second := h.expect(Started)
	h.expect(Ready)
	if second.PID == first.PID {
		t.Error("после падения должен быть новый процесс")
	}
	if st := h.sup.Status(); st.Restarts != 1 || st.PID != second.PID {
		t.Errorf("status: %+v", st)
	}
	if enginetest.Starts(h.dir) != 2 {
		t.Errorf("движок запускался %d раз, жду 2", enginetest.Starts(h.dir))
	}
}

func TestGrowingPauseThenCrashLoop(t *testing.T) {
	h := newHarness(t, "crash", nil)
	h.sup.Start()
	want := []time.Duration{40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond, 320 * time.Millisecond, 0}
	var last time.Time
	for i, pause := range want {
		st := h.expect(Started)
		ex := h.expect(Exited)
		if ex.RetryIn != pause || ex.Crashes != i+1 || ex.ExitCode != 3 {
			t.Fatalf("падение %d: %+v, жду паузу %v", i+1, ex, pause)
		}
		// The pause before this start must really have been waited out.
		if i > 0 {
			if gap := st.At.Sub(last); gap < want[i-1] {
				t.Errorf("перед запуском %d ждал %v, обещана пауза %v", i+1, gap, want[i-1])
			}
		}
		last = ex.At
	}
	failed := h.expect(Failed)
	if failed.Reason != FailCrashLoop {
		t.Errorf("причина %q, жду crashloop", failed.Reason)
	}
	if n := enginetest.Starts(h.dir); n != 5 {
		t.Errorf("запусков %d, жду 5", n)
	}
	select {
	case ev := <-h.events:
		t.Errorf("после crashloop пришло событие %v", ev.Kind)
	case <-time.After(500 * time.Millisecond):
	}

	// A new Start is a fresh attempt with a fresh count.
	h.sup.Start()
	h.expect(Started)
	if ex := h.expect(Exited); ex.Crashes != 1 || ex.RetryIn != 40*time.Millisecond {
		t.Errorf("после нового старта счёт должен начаться заново: %+v", ex)
	}
}

func TestRecoversAfterTwoCrashes(t *testing.T) {
	h := newHarness(t, "crash,crash,ok", nil)
	h.sup.Start()
	for i := 0; i < 2; i++ {
		h.expect(Started)
		h.expect(Exited)
	}
	h.expect(Started)
	h.expect(Ready)
	if st := h.sup.Status(); !st.Running || st.Restarts != 2 {
		t.Errorf("status: %+v", st)
	}
}

func TestStopKillsAHungProcess(t *testing.T) {
	h := newHarness(t, "hang", func(c *Config) { c.StopGrace = 300 * time.Millisecond })
	h.sup.Start()
	started := h.expect(Started)
	h.expect(Ready)

	asked := time.Now()
	h.sup.Stop()
	h.expect(Stopped)
	if took := time.Since(asked); took > 3*time.Second {
		t.Errorf("остановка зависшего процесса заняла %v", took)
	}
	waitGone(t, started.PID)
}

func TestMuteEngineIsRestarted(t *testing.T) {
	h := newHarness(t, "mute,ok", func(c *Config) { c.ReadyTimeout = 400 * time.Millisecond })
	h.sup.Start()
	first := h.expect(Started)
	ex := h.expect(Exited)
	if !ex.NotReady || ex.Uptime < 400*time.Millisecond {
		t.Errorf("молчащий движок: %+v, жду NotReady после 400 мс", ex)
	}
	waitGone(t, first.PID)
	h.expect(Started)
	h.expect(Ready)
}

func TestRestartCommand(t *testing.T) {
	h := newHarness(t, "ok", nil)
	h.sup.Start()
	first := h.expect(Started)
	h.expect(Ready)
	h.sup.Restart()
	second := h.expect(Started)
	h.expect(Ready)
	if first.PID == second.PID {
		t.Error("перезапуск должен дать новый процесс")
	}
	waitGone(t, first.PID)
	if st := h.sup.Status(); st.Restarts != 0 {
		t.Errorf("перезапуск по команде — не падение: %+v", st)
	}
}

func TestStopDuringThePauseBeforeRestart(t *testing.T) {
	h := newHarness(t, "crash", func(c *Config) { c.Policy.Delays = []time.Duration{time.Minute} })
	h.sup.Start()
	h.expect(Started)
	h.expect(Exited)
	asked := time.Now()
	h.sup.Stop()
	h.expect(Stopped)
	if took := time.Since(asked); took > 2*time.Second {
		t.Errorf("остановка во время паузы заняла %v", took)
	}
	if n := enginetest.Starts(h.dir); n != 1 {
		t.Errorf("после остановки движок запускался ещё: %d", n)
	}
}

func TestMissingBinaryAndBrokenPrepare(t *testing.T) {
	h := newHarness(t, "ok", func(c *Config) {
		c.Prepare = func() (Launch, func(context.Context) error, error) {
			return Launch{Exe: filepath.Join(t.TempDir(), "sing-box-which-is-not-there.exe")}, nil, nil
		}
	})
	h.sup.Start()
	if ev := h.expect(Failed); ev.Reason != FailMissing || ev.Err == nil {
		t.Errorf("нет файла движка: %+v, жду missing", ev)
	}

	h = newHarness(t, "ok", func(c *Config) {
		c.Prepare = func() (Launch, func(context.Context) error, error) {
			return Launch{}, nil, errors.New("config is not JSON")
		}
	})
	h.sup.Start()
	if ev := h.expect(Failed); ev.Reason != FailConfig {
		t.Errorf("битый конфиг: %+v, жду config", ev)
	}
}

// parentHelper is the body of a helper process: it plays the daemon, starts
// a supervised fake engine, reports its PID and then waits to be killed.
func parentHelper(dir string) {
	cfg := fakeConfig("ok", dir, nil)
	ready := make(chan int, 1)
	cfg.OnEvent = func(ev Event) {
		if ev.Kind == Ready {
			ready <- ev.PID
		}
	}
	sup := New(cfg)
	sup.Start()
	fmt.Printf("ENGINE %d\n", <-ready)
	select {}
}

// The engine must not outlive a daemon that died without cleaning up.
func TestNoOrphanAfterParentIsKilled(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(self)
	parent.Env = append(os.Environ(), envParentHelper+"="+t.TempDir())
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer parent.Process.Kill()

	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(out).ReadString('\n')
		line <- s
	}()
	var enginePID int
	select {
	case s := <-line:
		enginePID, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(s, "ENGINE ")))
	case <-time.After(20 * time.Second):
		t.Fatal("помощник не сообщил PID движка")
	}
	if enginePID == 0 || !Alive(enginePID) {
		t.Fatalf("движок помощника не работает (pid %d)", enginePID)
	}

	// No goodbye, no deferred cleanup: the way a crash or Task Manager does it.
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	parent.Wait()
	waitGone(t, enginePID)
}

// Two copies of one binary under one name in different folders: ending the
// first by path must leave the second alone. This is the developer's machine
// in miniature, where the second copy is the user's working VPN.
func TestKillByPathLeavesTheSameNameElsewhereAlone(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := "fake-sing-box"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	start := func() (path string, cmd *exec.Cmd) {
		dir := t.TempDir()
		path = filepath.Join(dir, name)
		copyFile(t, self, path)
		port, err := FreePort()
		if err != nil {
			t.Fatal(err)
		}
		api := fmt.Sprintf("127.0.0.1:%d", port)
		cmd = exec.Command(path)
		cmd.Env = enginetest.Env("ok", dir, api, "")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cmd.Process.Kill()
			cmd.Wait() // lets TempDir remove the binary
		})
		cl := clash.New(api, "")
		defer cl.Close()
		for deadline := time.Now().Add(10 * time.Second); ; {
			if _, err := cl.Version(context.Background()); err == nil {
				return path, cmd
			}
			if time.Now().After(deadline) {
				t.Fatal("копия поддельного движка не запустилась")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	ours, oursCmd := start()
	_, theirsCmd := start()

	pids, err := FindByPath(ours)
	if err != nil || len(pids) != 1 || pids[0] != oursCmd.Process.Pid {
		t.Fatalf("FindByPath: %v, %v — жду только %d", pids, err, oursCmd.Process.Pid)
	}
	n, err := KillByPath(ours)
	if err != nil || n != 1 {
		t.Fatalf("KillByPath: снято %d, %v — жду 1", n, err)
	}
	waitGone(t, oursCmd.Process.Pid)
	if !Alive(theirsCmd.Process.Pid) {
		t.Fatal("снят процесс с тем же именем из другой папки")
	}
	if pids, _ := FindByPath(ours); len(pids) != 0 {
		t.Errorf("после KillByPath остались процессы: %v", pids)
	}
	if n, _ := KillByPath(filepath.Join(t.TempDir(), name)); n != 0 {
		t.Errorf("по пути, откуда ничего не запущено, снято %d процессов", n)
	}
}

// The test binary itself runs from the path it would be asked to clear.
func TestKillByPathSparesItself(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pids, err := FindByPath(self)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pid := range pids {
		found = found || pid == os.Getpid()
	}
	if !found {
		t.Errorf("FindByPath не нашёл сам тест (pid %d) среди %v", os.Getpid(), pids)
	}
	// Sibling packages run their own binaries; only this process has this
	// path, so nothing may be killed.
	if n, err := KillByPath(self); err != nil || n != 0 {
		t.Errorf("KillByPath(self): %d, %v", n, err)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}
