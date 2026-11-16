package netmon

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeNet is a network whose state the test sets by hand and whose change
// notifications the test fires itself.
type fakeNet struct {
	mu    sync.Mutex
	state State
	err   error
	reads int
	kick  func()
}

func (f *fakeNet) read() (State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.state, f.err
}

func (f *fakeNet) watch(kick func()) (func(), error) {
	f.kick = kick
	return func() {}, nil
}

func (f *fakeNet) set(s State) {
	f.mu.Lock()
	f.state = s
	f.mu.Unlock()
}

func (f *fakeNet) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func next(t *testing.T, ch <-chan State, what string) State {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(3 * time.Second):
		t.Fatalf("не дождался наблюдения: %s", what)
		return State{}
	}
}

func quiet(t *testing.T, ch <-chan State, d time.Duration, what string) {
	t.Helper()
	select {
	case s := <-ch:
		t.Fatalf("лишнее наблюдение (%s): %+v", what, s)
	case <-time.After(d):
	}
}

func TestBurstOfEventsIsOneObservation(t *testing.T) {
	f := &fakeNet{state: State{V4: true, via: "wifi"}}
	got := make(chan State, 16)
	m := start(60*time.Millisecond, f.read, f.watch, func(s State) { got <- s })
	defer m.Close()

	if s := next(t, got, "первое"); !s.V4 || s.V6 || !s.Up() {
		t.Fatalf("первое наблюдение: %+v", s)
	}

	// The cable is pulled: the system fires a dozen events in a row.
	f.set(State{})
	before := f.readCount()
	for i := 0; i < 12; i++ {
		f.kick()
		time.Sleep(time.Millisecond)
	}
	if s := next(t, got, "сеть пропала"); s.Up() {
		t.Fatalf("после пропажи сети: %+v", s)
	}
	quiet(t, got, 150*time.Millisecond, "одна смена сети")
	if n := f.readCount() - before; n != 1 {
		t.Errorf("на пачку событий маршруты прочитаны %d раз, жду один", n)
	}

	// Events that change nothing are not reported.
	f.kick()
	quiet(t, got, 150*time.Millisecond, "ничего не изменилось")

	// Another Wi-Fi: the same flags, a different way out.
	f.set(State{V4: true, via: "wifi-2"})
	f.kick()
	f.set(State{V4: true, via: "wifi-2"})
	if s := next(t, got, "другая сеть"); !s.Up() {
		t.Fatalf("после смены сети: %+v", s)
	}
}

func TestReadErrorKeepsTheLastAnswer(t *testing.T) {
	f := &fakeNet{state: State{V4: true}}
	got := make(chan State, 16)
	m := start(20*time.Millisecond, f.read, f.watch, func(s State) { got <- s })
	defer m.Close()
	next(t, got, "первое")

	f.mu.Lock()
	f.err = errors.New("table unavailable")
	f.mu.Unlock()
	f.kick()
	quiet(t, got, 100*time.Millisecond, "маршруты не прочитались")
}

func TestFallsBackToPollingWhenNotificationsFail(t *testing.T) {
	f := &fakeNet{state: State{V4: true}}
	failing := func(func()) (func(), error) { return nil, errors.New("no notifications") }
	got := make(chan State, 16)
	m := start(time.Millisecond, f.read, failing, func(s State) { got <- s })
	next(t, got, "первое")
	m.Close() // must stop the fallback poller without hanging
}

// The real thing: registers with the system, reads the routing table once,
// unregisters. What it sees depends on the machine, so only the mechanics
// are checked.
func TestRealMonitorStartsAndStops(t *testing.T) {
	got := make(chan State, 4)
	m := Start(50*time.Millisecond, []string{"Strelka"}, func(s State) { got <- s })
	s := next(t, got, "первое наблюдение настоящего монитора")
	t.Logf("маршрут по умолчанию: IPv4 %v, IPv6 %v (%s)", s.V4, s.V6, s.via)
	done := make(chan struct{})
	go func() { m.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("монитор не остановился")
	}
}
