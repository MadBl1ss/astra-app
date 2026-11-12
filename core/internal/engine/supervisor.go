// Package engine starts an engine process (sing-box, later Xray), keeps it
// confined to the daemon's lifetime, learns of its death at once and brings
// it back with growing pauses.
package engine

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Why an engine cannot be kept running. The values are those of
// State.vpn.failReason.
const (
	FailMissing   = "missing"   // the engine binary or its config is not there
	FailConfig    = "config"    // the config could not be prepared
	FailCrashLoop = "crashloop" // it keeps dying right after start
)

// EventKind says what happened to the engine.
type EventKind int

const (
	// Started: a process exists; it is not serving yet.
	Started EventKind = iota
	// Ready: the readiness check passed.
	Ready
	// Exited: the process died on its own or never became ready. A restart
	// follows after RetryIn unless a Failed event comes next.
	Exited
	// Stopped: the engine is down because Stop was asked.
	Stopped
	// Failed: the supervisor gave up; only Start or Restart brings it back.
	Failed
)

func (k EventKind) String() string {
	return [...]string{"started", "ready", "exited", "stopped", "failed"}[k]
}

// Event is one observation the supervisor reports to its owner.
type Event struct {
	Kind EventKind
	At   time.Time
	PID  int

	// Exited
	ExitCode int
	Uptime   time.Duration
	NotReady bool          // killed by the supervisor: it never answered the readiness check
	RetryIn  time.Duration // pause before the next start
	Crashes  int           // crashes inside the policy window, this one included
	Tail     []string      // last lines of output, the usual place of the reason

	// Failed
	Reason string // FailMissing | FailConfig | FailCrashLoop
	Err    error
}

// Config describes one supervised engine.
type Config struct {
	Name string // "sing-box"; used in logs only

	// Prepare is called before every start, the restarts included: it picks
	// a fresh API port and secret and writes the config. ready, if not nil,
	// is polled until it returns nil.
	Prepare func() (l Launch, ready func(context.Context) error, err error)

	LogFile string // engine output is appended here; empty keeps it in memory only

	Policy       Policy
	ReadyTimeout time.Duration // how long a started engine may stay silent
	ReadyPoll    time.Duration
	StopGrace    time.Duration // between the polite request and the kill

	// OnEvent and OnLine are called from the supervisor's goroutines and
	// must not block for long.
	OnEvent func(Event)
	OnLine  func(line string)
}

func (c *Config) defaults() {
	if c.Policy.Delays == nil && c.Policy.CrashLimit == 0 {
		c.Policy = DefaultPolicy()
	}
	if c.ReadyTimeout == 0 {
		c.ReadyTimeout = 20 * time.Second
	}
	if c.ReadyPoll == 0 {
		c.ReadyPoll = 150 * time.Millisecond
	}
	if c.StopGrace == 0 {
		c.StopGrace = 3 * time.Second
	}
	if c.OnEvent == nil {
		c.OnEvent = func(Event) {}
	}
	if c.OnLine == nil {
		c.OnLine = func(string) {}
	}
}

// Status is a snapshot for the debug method engine.status.
type Status struct {
	Running  bool
	PID      int
	Since    time.Time // start of the current process
	Restarts int       // crashes since the last Start or Restart
}

type command int

const (
	cmdStart command = iota
	cmdStop
	cmdRestart
	cmdClose
)

// Supervisor owns one engine. Start, Stop and Restart only record what is
// wanted and return; what then happens comes back as events.
type Supervisor struct {
	cfg      Config
	cmds     chan command
	finished chan struct{}
	ring     *ring
	logFile  *rotatingFile
	logMu    sync.Mutex

	mu     sync.Mutex
	status Status

	closeOnce sync.Once
}

// New starts the supervisor's goroutine. The engine stays down until Start.
func New(cfg Config) *Supervisor {
	cfg.defaults()
	s := &Supervisor{
		cfg:      cfg,
		cmds:     make(chan command, 16),
		finished: make(chan struct{}),
		ring:     newRing(ringLines),
	}
	if cfg.LogFile != "" {
		s.logFile = openRotating(cfg.LogFile, logFileLimit)
	}
	go s.run()
	return s
}

func (s *Supervisor) Start()   { s.send(cmdStart) }
func (s *Supervisor) Stop()    { s.send(cmdStop) }
func (s *Supervisor) Restart() { s.send(cmdRestart) }

// Close stops the engine and waits until the supervisor is gone. No events
// are promised after it is called.
func (s *Supervisor) Close() {
	s.closeOnce.Do(func() { s.send(cmdClose) })
	<-s.finished
}

func (s *Supervisor) send(c command) {
	select {
	case s.cmds <- c:
	case <-s.finished:
	}
}

// Status is safe to call from any goroutine.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Tail returns up to n last lines of the engine's output.
func (s *Supervisor) Tail(n int) []string { return s.ring.tail(n) }

func (s *Supervisor) setStatus(fn func(*Status)) {
	s.mu.Lock()
	fn(&s.status)
	s.mu.Unlock()
}

func (s *Supervisor) emit(ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	s.cfg.OnEvent(ev)
}

func (s *Supervisor) line(line string) {
	s.ring.add(line)
	s.logMu.Lock()
	s.logFile.writeLine(line)
	s.logMu.Unlock()
	s.cfg.OnLine(line)
}

// what a session or a watch ended with
type outcome int

const (
	outIdle    outcome = iota // engine is down and should stay down
	outClose                  // the supervisor is shutting down
	outExited                 // the process died on its own
	outStop                   // Stop was asked and done
	outRestart                // Restart was asked; the process is down, start again
)

func (s *Supervisor) run() {
	defer close(s.finished)
	defer func() {
		s.logMu.Lock()
		s.logFile.close()
		s.logMu.Unlock()
	}()
	for {
		switch <-s.cmds {
		case cmdClose:
			return
		case cmdStop:
			// Already down. The owner still waits for the confirmation.
			s.emit(Event{Kind: Stopped})
		case cmdStart, cmdRestart:
			if s.session() == outClose {
				return
			}
		}
	}
}

// session keeps the engine running until it is told to stop or gives up.
func (s *Supervisor) session() outcome {
	rs := newRestarts(s.cfg.Policy)
	s.setStatus(func(st *Status) { st.Restarts = 0 })
	for {
		p, ready, err := s.launch()
		if err != nil {
			if reason := fatalReason(err); reason != "" {
				s.emit(Event{Kind: Failed, Reason: reason, Err: err})
				return outIdle
			}
			// Anything else (the file is locked by an antivirus scan, the
			// system is out of handles) may pass; it is retried as a crash.
			s.line("strelkad: " + s.cfg.Name + " не запустился: " + err.Error())
		}

		out := outExited
		ev := Event{Kind: Exited, ExitCode: -1}
		if p != nil {
			s.setStatus(func(st *Status) { st.Running, st.PID, st.Since = true, p.pid, p.started })
			s.emit(Event{Kind: Started, PID: p.pid, At: p.started})
			var notReady bool
			out, notReady = s.watch(p, ready)
			ev.PID, ev.ExitCode, ev.NotReady = p.pid, p.exitCode(), notReady
			ev.Uptime = time.Since(p.started)
			s.setStatus(func(st *Status) { st.Running, st.PID, st.Since = false, 0, time.Time{} })
		}

		switch out {
		case outClose:
			return outClose
		case outStop:
			s.emit(Event{Kind: Stopped})
			return outIdle
		case outRestart:
			rs = newRestarts(s.cfg.Policy)
			s.setStatus(func(st *Status) { st.Restarts = 0 })
			continue
		}

		delay, loop := rs.crashed(time.Now(), ev.Uptime)
		ev.RetryIn, ev.Crashes, ev.Tail = delay, rs.count(), s.ring.tail(20)
		s.setStatus(func(st *Status) { st.Restarts++ })
		s.emit(ev)
		if loop {
			s.emit(Event{Kind: Failed, Reason: FailCrashLoop})
			return outIdle
		}

		switch s.pause(delay) {
		case outClose:
			return outClose
		case outStop:
			s.emit(Event{Kind: Stopped})
			return outIdle
		case outRestart:
			rs = newRestarts(s.cfg.Policy)
			s.setStatus(func(st *Status) { st.Restarts = 0 })
		}
	}
}

// fatalReason picks out the errors that no amount of retrying will fix.
func fatalReason(err error) string {
	var pe *PrepareError
	if errors.As(err, &pe) {
		return pe.Reason
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, exec.ErrNotFound) {
		return FailMissing
	}
	return ""
}

// PrepareError lets Prepare say why a start is pointless.
type PrepareError struct {
	Reason string // FailMissing | FailConfig
	Err    error
}

func (e *PrepareError) Error() string { return e.Err.Error() }
func (e *PrepareError) Unwrap() error { return e.Err }

// launch prepares and starts one process and begins reading its output.
func (s *Supervisor) launch() (*process, func(context.Context) error, error) {
	l, ready, err := s.cfg.Prepare()
	if err != nil {
		var pe *PrepareError
		if !errors.As(err, &pe) && !errors.Is(err, fs.ErrNotExist) {
			err = &PrepareError{Reason: FailConfig, Err: err}
		}
		return nil, nil, err
	}
	if _, err := os.Stat(l.Exe); err != nil {
		return nil, nil, err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	p, err := startProcess(l, pw)
	// The child holds its own copy of the write end; ours must go, or the
	// reader would never see EOF when the child exits.
	pw.Close()
	if err != nil {
		pr.Close()
		return nil, nil, err
	}
	drained := make(chan struct{})
	go func() {
		readLines(pr, s.line)
		pr.Close()
		close(drained)
	}()
	p.drained = drained
	return p, ready, nil
}

// watch follows a running process until it exits or a command ends it.
func (s *Supervisor) watch(p *process, ready func(context.Context) error) (out outcome, notReady bool) {
	defer s.reap(p)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readyCh := make(chan struct{})
	go func() {
		for ready != nil {
			if ready(ctx) == nil {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.cfg.ReadyPoll):
			}
		}
		close(readyCh)
	}()
	deadline := time.NewTimer(s.cfg.ReadyTimeout)
	defer deadline.Stop()

	for {
		select {
		case <-p.exited:
			return outExited, false
		case <-readyCh:
			readyCh = nil
			deadline.Stop()
			s.emit(Event{Kind: Ready, PID: p.pid})
		case <-deadline.C:
			if readyCh == nil {
				continue // the timer fired in the same instant the check passed
			}
			// A process that is up but mute is as useless as a dead one,
			// and only a restart can change that.
			s.line("strelkad: " + s.cfg.Name + " не ответил за " + s.cfg.ReadyTimeout.String() + ", перезапускаю")
			p.kill()
			<-p.exited
			return outExited, true
		case c := <-s.cmds:
			switch c {
			case cmdStart:
				continue
			case cmdStop:
				s.terminate(p)
				return outStop, false
			case cmdRestart:
				s.terminate(p)
				return outRestart, false
			case cmdClose:
				s.terminate(p)
				return outClose, false
			}
		}
	}
}

// terminate asks the process to stop and kills it if it does not listen.
func (s *Supervisor) terminate(p *process) {
	select {
	case <-p.exited:
		// Already gone: do not signal a PID that is no longer ours.
		return
	default:
	}
	if err := p.softStop(); err == nil {
		grace := time.NewTimer(s.cfg.StopGrace)
		defer grace.Stop()
		select {
		case <-p.exited:
			return
		case <-grace.C:
		}
	}
	p.kill()
	<-p.exited
}

// reap runs once the process is gone: it lets the output reader finish, so
// the last words of a crash are in the ring before the event goes out, and
// closes the job.
func (s *Supervisor) reap(p *process) {
	select {
	case <-p.drained:
	case <-time.After(500 * time.Millisecond):
		// Something else still holds the pipe; do not wait for it.
	}
	p.release()
}

// pause waits out the delay before a restart, still listening to commands.
func (s *Supervisor) pause(d time.Duration) outcome {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			return outExited
		case c := <-s.cmds:
			switch c {
			case cmdStop:
				return outStop
			case cmdRestart:
				return outRestart
			case cmdClose:
				return outClose
			}
		}
	}
}
