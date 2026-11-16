// Package netmon watches the one thing about the network the daemon needs
// to know before it blames a server: is there a way out at all. It reports
// whether a default route exists, and reports again when that changes or
// when the route moves to another adapter or gateway.
package netmon

import "time"

// State is one observation.
type State struct {
	V4, V6 bool // a default route exists for the family
	// via names the adapters and gateways behind the routes. Two states with
	// the same flags and a different via are a change of network: the laptop
	// moved from one Wi-Fi to another.
	via string
}

// Up reports whether there is any default route.
func (s State) Up() bool { return s.V4 || s.V6 }

// pollEvery is the pace where the system offers no notifications (Linux
// until stage 09 brings netlink), and the fallback if they fail to register.
const pollEvery = 5 * time.Second

// Monitor is a running watch. Close stops it.
type Monitor struct {
	kicks chan struct{}
	quit  chan struct{}
	done  chan struct{}
	stop  func()
}

// Start begins watching. onChange is called from the monitor's goroutine
// with the first observation and then with every different one. Events are
// merged over debounce: a single network change fires a dozen of them.
// ignore lists adapters whose routes do not count - our own tunnel is not a
// way out.
func Start(debounce time.Duration, ignore []string, onChange func(State)) *Monitor {
	return start(debounce, func() (State, error) { return read(ignore) }, watch, onChange)
}

func start(debounce time.Duration, read func() (State, error), watch func(kick func()) (func(), error), onChange func(State)) *Monitor {
	m := &Monitor{kicks: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{})}
	kick := func() {
		select {
		case m.kicks <- struct{}{}:
		default: // one is already waiting; that is enough
		}
	}
	stop, err := watch(kick)
	if err != nil {
		stop = poll(kick, pollEvery)
	}
	m.stop = stop

	go func() {
		defer close(m.done)
		var last State
		known := false
		look := func() {
			st, err := read()
			if err != nil {
				return // keep the last answer rather than guess
			}
			if !known || st != last {
				last, known = st, true
				onChange(st)
			}
		}
		look()
		var due <-chan time.Time
		for {
			select {
			case <-m.quit:
				return
			case <-m.kicks:
				if due == nil {
					due = time.After(debounce)
				}
			case <-due:
				due = nil
				look()
			}
		}
	}()
	return m
}

// poll kicks at a fixed pace until stopped.
func poll(kick func(), every time.Duration) func() {
	quit := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
				kick()
			}
		}
	}()
	return func() { close(quit) }
}

// Close stops the watch and waits for the goroutine. It must not be called
// from onChange.
func (m *Monitor) Close() {
	m.stop()
	close(m.quit)
	<-m.done
}
