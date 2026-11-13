// Package daemon is the orchestrator: one loop owns the State document.
// Commands from clients and observations from subsystems come in, a new
// State goes out. Nothing else reads or writes the state, so there are no
// locks around it and the logic can be followed one message at a time.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"runtime"
	"time"

	"strelka/internal/clash"
	"strelka/internal/engine"
	"strelka/internal/ipc"
	"strelka/internal/store"
	"strelka/proto"
)

// Version of the daemon, reported in hello and in State.
const Version = "0.1.0"

// minStateInterval caps the broadcast at ten documents a second; changes
// that come faster are merged into the next one.
const minStateInterval = 100 * time.Millisecond

const engineName = "sing-box"

// Options configure a daemon. The zero value of every optional field is the
// production behaviour; tests override the engine command and the timings.
type Options struct {
	Paths store.Paths

	// Config is the ready engine config to start from. Empty means
	// run/config.json in the data folder. Stage 02 replaces this with a
	// config built from the profile.
	Config string

	// Launch overrides how the engine is started for a prepared config.
	Launch func(configPath string) engine.Launch

	Policy       engine.Policy
	ReadyTimeout time.Duration
	StopGrace    time.Duration

	// Logf receives the daemon's own diagnostics (English, for the console).
	Logf func(format string, a ...any)
}

type desiredFile struct {
	Desired string `json:"desired"`
}

type rpcCall struct {
	conn   *ipc.Conn
	method string
	params json.RawMessage
	reply  chan rpcReply
}

type rpcReply struct {
	result any
	err    *proto.Error
}

// Daemon is one running service.
type Daemon struct {
	opts Options
	srv  *ipc.Server
	sup  *engine.Supervisor

	inbox chan any      // rpcCall and engine.Event
	done  chan struct{} // closed when the loop has exited

	// Everything below belongs to the loop goroutine.
	st         proto.State
	journal    *journal
	dirty      bool
	lastSent   time.Time
	flush      *time.Timer
	flushArmed bool
	everOn     bool // the engine was ready at least once since the last start command
}

// New prepares a daemon: folders, the saved desired state, the journal. It
// also clears engine processes a previous daemon may have left behind. The
// caller should hold the channel's listener by now, so that a second daemon
// fails on the channel before it gets to kill the first one's engine.
func New(opts Options) (*Daemon, error) {
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	if err := opts.Paths.Ensure(); err != nil {
		return nil, fmt.Errorf("data folder: %w", err)
	}
	d := &Daemon{
		opts:  opts,
		inbox: make(chan any, 256),
		done:  make(chan struct{}),
		st:    proto.NewState(Version),
	}

	saved, err := store.LoadJSON(opts.Paths.DesiredFile(), desiredFile{Desired: proto.DesiredOff})
	if err != nil {
		// A broken file must not keep the daemon down; off is the safe read.
		opts.Logf("desired state unreadable, assuming off: %v", err)
	}
	if saved.Desired == proto.DesiredOn {
		d.st.VPN.Desired = proto.DesiredOn
	}

	exe := opts.Paths.Engine(engineName)
	if n, err := engine.KillByPath(exe); err != nil {
		opts.Logf("looking for stray %s: %v", engineName, err)
	} else if n > 0 {
		opts.Logf("ended %d stray %s left by a previous run (%s)", n, engineName, exe)
	}

	d.journal = openJournal(opts.Paths.Journal())
	d.srv = ipc.NewServer(d.handle)
	d.sup = engine.New(engine.Config{
		Name:         engineName,
		Prepare:      d.prepare,
		LogFile:      opts.Paths.EngineLog(engineName),
		Policy:       opts.Policy,
		ReadyTimeout: opts.ReadyTimeout,
		StopGrace:    opts.StopGrace,
		OnEvent: func(ev engine.Event) {
			select {
			case d.inbox <- ev:
			case <-d.done:
			}
		},
		OnLine: func(line string) {
			d.srv.Publish(proto.TopicLog, proto.LogLine{
				T: time.Now().UTC(), Source: engineName, Level: engine.Level(line), Line: line,
			})
		},
	})
	return d, nil
}

// prepare runs before every engine start, in the supervisor's goroutine. It
// reads only immutable options, never the loop's state.
func (d *Daemon) prepare() (engine.Launch, func(context.Context) error, error) {
	src := d.opts.Config
	if src == "" {
		src = d.opts.Paths.RunConfig()
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		reason := engine.FailConfig
		if errors.Is(err, fs.ErrNotExist) {
			reason = engine.FailMissing
		}
		return engine.Launch{}, nil, &engine.PrepareError{Reason: reason, Err: err}
	}
	// A fresh port and secret every time: yesterday's port may sit in a
	// range Windows reserved overnight, and nobody else needs the secret.
	port, err := engine.FreePort()
	if err != nil {
		return engine.Launch{}, nil, err
	}
	secret, err := engine.NewSecret()
	if err != nil {
		return engine.Launch{}, nil, err
	}
	controller := fmt.Sprintf("127.0.0.1:%d", port)
	patched, err := patchConfig(raw, controller, secret)
	if err != nil {
		return engine.Launch{}, nil, &engine.PrepareError{Reason: engine.FailConfig, Err: err}
	}
	cfgPath := d.opts.Paths.RunConfig()
	if err := store.WriteAtomic(cfgPath, patched, 0o600); err != nil {
		return engine.Launch{}, nil, err
	}

	var l engine.Launch
	if d.opts.Launch != nil {
		l = d.opts.Launch(cfgPath)
	} else {
		l = engine.Launch{
			Exe: d.opts.Paths.Engine(engineName),
			// --disable-color: stderr is a pipe read by the daemon, not a terminal.
			Args: []string{"run", "-c", cfgPath, "-D", d.opts.Paths.Data, "--disable-color"},
			Dir:  d.opts.Paths.Data,
		}
	}
	api := clash.New(controller, secret)
	ready := func(ctx context.Context) error {
		_, err := api.Version(ctx)
		return err
	}
	return l, ready, nil
}

// Run serves clients on l and runs the loop until ctx is cancelled, then
// stops the engine and returns. The desired state is not touched on the way
// out: the next run picks up where this one left.
func (d *Daemon) Run(ctx context.Context, l net.Listener) error {
	served := make(chan error, 1)
	go func() { served <- d.srv.Serve(l) }()

	d.loop(ctx)

	// Let the last State (phase off) reach the clients before they are cut.
	d.srv.Drain(500 * time.Millisecond)
	l.Close()
	d.srv.Close()
	d.sup.Close()
	d.journal.close()
	return <-served
}

func (d *Daemon) loop(ctx context.Context) {
	defer close(d.done)
	d.flush = time.NewTimer(time.Hour)
	d.flush.Stop()

	d.log(proto.CatSystem, proto.LevelInfo, "Служба запущена.")
	if d.st.VPN.Desired == proto.DesiredOn && d.st.Settings.StartOnBoot {
		d.beginStart()
	}
	d.touch()

	stopping := ctx.Done()
	var giveUp <-chan time.Time
	for {
		select {
		case msg := <-d.inbox:
			switch m := msg.(type) {
			case rpcCall:
				if stopping == nil {
					m.reply <- rpcReply{err: proto.Errorf(proto.CodeBusy, "Служба завершает работу.")}
					continue
				}
				result, err := d.dispatch(m)
				m.reply <- rpcReply{result: result, err: err}
			case engine.Event:
				d.onEngine(m)
			}
			if stopping == nil && d.st.VPN.Phase == proto.PhaseOff {
				d.shutdown()
				return
			}
		case <-d.flush.C:
			d.flushArmed = false
			d.publish()
		case <-stopping:
			stopping = nil // handled once
			if d.st.VPN.Phase == proto.PhaseOff {
				d.shutdown()
				return
			}
			d.opts.Logf("shutting down: stopping %s", engineName)
			d.setPhase(proto.PhaseStopping)
			d.setBusy(proto.BusyStopping)
			d.touch()
			d.sup.Stop()
			// The supervisor kills after its own grace; this is the belt to
			// those braces, so a bug there cannot hang the shutdown.
			giveUp = time.After(15 * time.Second)
		case <-giveUp:
			d.opts.Logf("shutting down: %s did not confirm the stop", engineName)
			d.shutdown()
			return
		}
	}
}

func (d *Daemon) shutdown() {
	d.log(proto.CatSystem, proto.LevelInfo, "Служба остановлена.")
	d.publish()
}

// handle is the ipc handler. It runs in a connection's goroutine and only
// hands the call to the loop.
func (d *Daemon) handle(c *ipc.Conn, method string, params json.RawMessage) (any, *proto.Error) {
	call := rpcCall{conn: c, method: method, params: params, reply: make(chan rpcReply, 1)}
	gone := proto.Errorf(proto.CodeBusy, "Служба завершает работу.")
	select {
	case d.inbox <- call:
	case <-d.done:
		return nil, gone
	}
	select {
	case r := <-call.reply:
		return r.result, r.err
	case <-d.done:
		return nil, gone
	}
}

func (d *Daemon) dispatch(c rpcCall) (any, *proto.Error) {
	switch c.method {
	case proto.MethodHello:
		return proto.HelloResult{Version: Version, Proto: proto.Version, OS: runtime.GOOS}, nil
	case proto.MethodStateGet:
		return d.snapshot(), nil
	case proto.MethodStateSubscribe, proto.MethodStateUnsubscribe:
		return d.subscription(c)
	case proto.MethodVPNStart:
		return d.vpnStart()
	case proto.MethodVPNStop:
		return d.vpnStop()
	case proto.MethodVPNRestart:
		return d.vpnRestart()
	case proto.MethodEngineStatus:
		return d.engineStatus(), nil
	}
	return nil, proto.Errorf(proto.CodeMethodNotFound, "Служба не знает метод «%s».", c.method)
}

func (d *Daemon) subscription(c rpcCall) (any, *proto.Error) {
	var p proto.TopicsParams
	if len(c.params) > 0 {
		if err := json.Unmarshal(c.params, &p); err != nil {
			return nil, proto.Errorf(proto.CodeInvalidParams, "Неверные параметры: нужен список тем.")
		}
	}
	for _, t := range p.Topics {
		if !proto.KnownTopic(t) {
			return nil, proto.Errorf(proto.CodeInvalidParams, "Нет такой темы: «%s».", t)
		}
	}
	if c.method == proto.MethodStateUnsubscribe {
		c.conn.Unsubscribe(p.Topics...)
		return nil, nil
	}
	c.conn.Subscribe(p.Topics...)
	for _, t := range p.Topics {
		if t == proto.TopicState {
			// The current document right away, from the loop: a client
			// never sits on a stale state waiting for the next change, and
			// it cannot arrive out of order with a broadcast.
			c.conn.Notify(proto.TopicState, d.snapshot())
		}
	}
	return nil, nil
}

func (d *Daemon) vpnStart() (any, *proto.Error) {
	switch d.st.VPN.Phase {
	case proto.PhaseStopping:
		return nil, proto.Errorf(proto.CodeBusy, "VPN ещё выключается. Подожди пару секунд и попробуй снова.")
	case proto.PhaseStarting, proto.PhaseOn, proto.PhaseRecovering:
		d.setDesired(proto.DesiredOn)
		return nil, nil
	}
	d.setDesired(proto.DesiredOn)
	d.beginStart()
	return nil, nil
}

func (d *Daemon) vpnStop() (any, *proto.Error) {
	d.setDesired(proto.DesiredOff)
	switch d.st.VPN.Phase {
	case proto.PhaseOff, proto.PhaseStopping:
		return nil, nil
	}
	d.setPhase(proto.PhaseStopping)
	d.setBusy(proto.BusyStopping)
	d.touch()
	d.sup.Stop()
	return nil, nil
}

func (d *Daemon) vpnRestart() (any, *proto.Error) {
	switch d.st.VPN.Phase {
	case proto.PhaseOff:
		return nil, proto.Errorf(proto.CodeInvalid, "VPN выключен. Сначала включи его.")
	case proto.PhaseStarting:
		return nil, proto.Errorf(proto.CodeBusy, "VPN ещё включается. Подожди пару секунд и попробуй снова.")
	case proto.PhaseStopping:
		return nil, proto.Errorf(proto.CodeBusy, "VPN ещё выключается. Подожди пару секунд и попробуй снова.")
	}
	d.log(proto.CatVPN, proto.LevelInfo, "Перезапускаю VPN.")
	d.markStarting()
	d.sup.Restart()
	return nil, nil
}

func (d *Daemon) beginStart() {
	d.markStarting()
	d.sup.Start()
}

func (d *Daemon) markStarting() {
	d.everOn = false
	d.setPhase(proto.PhaseStarting)
	d.setBusy(proto.BusyStarting)
	d.st.VPN.FailReason, d.st.VPN.FailText = nil, nil
	d.touch()
}

func (d *Daemon) engineStatus() proto.EngineStatusResult {
	st := d.sup.Status()
	es := proto.EngineStatus{
		Name:     engineName,
		Path:     d.opts.Paths.Engine(engineName),
		Running:  st.Running,
		PID:      st.PID,
		Restarts: st.Restarts,
	}
	if st.Running {
		since := st.Since.UTC()
		es.Since = &since
	}
	return proto.EngineStatusResult{Engines: []proto.EngineStatus{es}}
}

// onEngine applies one observation from the supervisor.
func (d *Daemon) onEngine(ev engine.Event) {
	switch ev.Kind {
	case engine.Started:
		d.opts.Logf("%s started, pid %d", engineName, ev.PID)

	case engine.Ready:
		d.opts.Logf("%s is ready, pid %d", engineName, ev.PID)
		if d.st.VPN.Phase == proto.PhaseStopping {
			return // a stop is already on its way to this process
		}
		text := "VPN включён."
		if d.everOn {
			text = "VPN снова работает."
		}
		d.everOn = true
		d.setPhase(proto.PhaseOn)
		d.setBusy("")
		d.log(proto.CatVPN, proto.LevelInfo, text)
		d.touch()

	case engine.Exited:
		d.opts.Logf("%s exited: code %d, uptime %v, not ready %v, retry in %v",
			engineName, ev.ExitCode, ev.Uptime.Round(time.Millisecond), ev.NotReady, ev.RetryIn)
		for _, line := range lastLines(ev.Tail, 5) {
			d.opts.Logf("    %s", line)
		}
		if d.st.VPN.Phase == proto.PhaseStopping {
			return
		}
		// Desired stays as it is: a crash is not the user's wish to be off.
		if d.st.VPN.Phase == proto.PhaseOn {
			d.setPhase(proto.PhaseRecovering)
		}
		// The exit code is in the daemon's log; it tells the user nothing.
		what := "VPN неожиданно остановился."
		if ev.NotReady {
			what = "VPN запустился, но не отвечает."
		}
		if ev.RetryIn > 0 {
			what += fmt.Sprintf(" Перезапускаю через %s.", russianDelay(ev.RetryIn))
		}
		d.log(proto.CatVPN, proto.LevelProblem, what)
		d.touch()

	case engine.Stopped:
		how := "cleanly"
		if ev.Forced {
			how = "by force: it did not take the polite request"
		}
		if ev.PID != 0 {
			d.opts.Logf("%s stopped %s, pid %d", engineName, how, ev.PID)
		}
		if d.st.VPN.Phase == proto.PhaseOff {
			return
		}
		d.setPhase(proto.PhaseOff)
		d.setBusy("")
		d.st.VPN.FailReason, d.st.VPN.FailText = nil, nil
		d.log(proto.CatVPN, proto.LevelInfo, "VPN выключен.")
		d.touch()

	case engine.Failed:
		d.opts.Logf("%s failed: %s: %v", engineName, ev.Reason, ev.Err)
		if d.st.VPN.Phase == proto.PhaseStopping {
			// Giving up is as good as stopped for someone who asked to stop.
			d.setPhase(proto.PhaseOff)
			d.setBusy("")
			d.log(proto.CatVPN, proto.LevelInfo, "VPN выключен.")
			d.touch()
			return
		}
		reason, text := ev.Reason, failText(ev.Reason)
		d.setPhase(proto.PhaseFailed)
		d.setBusy("")
		d.st.VPN.FailReason, d.st.VPN.FailText = &reason, &text
		d.log(proto.CatVPN, proto.LevelCritical, "VPN не запускается. "+text)
		d.touch()
	}
}

func failText(reason string) string {
	switch reason {
	case engine.FailMissing:
		return "Не хватает файлов Стрелки: нет движка sing-box или его настроек. Переустанови приложение."
	case engine.FailConfig:
		return "Настройки VPN повреждены, запустить с ними не получается."
	case engine.FailCrashLoop:
		return "VPN падает сразу после запуска. Подробности — в журнале."
	}
	return "Причина неизвестна. Подробности — в журнале."
}

// russianDelay words a restart pause: "1 с", "30 с"; test-sized pauses come out in ms.
func russianDelay(d time.Duration) string {
	if d >= time.Second && d%time.Second == 0 {
		return fmt.Sprintf("%d с", int(d/time.Second))
	}
	return fmt.Sprintf("%d мс", d.Milliseconds())
}

func lastLines(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

// setPhase moves the phase and the fields that are functions of it. Health
// proper arrives in stage 03; until then the verdict only says what the
// engine itself is doing.
func (d *Daemon) setPhase(phase string) {
	if d.st.VPN.Phase == phase {
		return
	}
	d.st.VPN.Phase = phase
	now := time.Now().UTC().Truncate(time.Second)
	d.st.VPN.Since = nil
	if phase == proto.PhaseOn {
		d.st.VPN.Since = &now
	}
	verdict := proto.VerdictSettling
	switch phase {
	case proto.PhaseRecovering:
		verdict = proto.VerdictEngineRecovering
	case proto.PhaseFailed:
		verdict = proto.VerdictEngineFailed
	}
	if d.st.Health.Verdict != verdict {
		d.st.Health.Verdict = verdict
		d.st.Health.Since = &now
	}
}

func (d *Daemon) setBusy(busy string) {
	if busy == "" {
		d.st.Busy = nil
		return
	}
	d.st.Busy = &busy
}

// setDesired records the user's wish and saves it at once: it must survive
// a power cut that comes a second later.
func (d *Daemon) setDesired(desired string) {
	if d.st.VPN.Desired == desired {
		return
	}
	d.st.VPN.Desired = desired
	if err := store.SaveJSON(d.opts.Paths.DesiredFile(), desiredFile{Desired: desired}); err != nil {
		d.opts.Logf("saving desired state: %v", err)
	}
	d.touch()
}

func (d *Daemon) log(cat, level, text string) {
	e := d.journal.add(time.Now(), cat, level, text)
	d.srv.Publish(proto.TopicJournal, e)
}

// touch marks the state changed and makes sure a broadcast is scheduled.
func (d *Daemon) touch() {
	d.st.Rev++
	d.dirty = true
	if d.flushArmed {
		return
	}
	wait := minStateInterval - time.Since(d.lastSent)
	if wait < 0 {
		// Even "now" goes through the timer: the message being handled may
		// touch the state again, and all of it should leave as one document.
		wait = 0
	}
	d.flush.Reset(wait)
	d.flushArmed = true
}

func (d *Daemon) publish() {
	if !d.dirty {
		return
	}
	d.dirty = false
	d.lastSent = time.Now()
	d.srv.Publish(proto.TopicState, d.snapshot())
}

// snapshot freezes the state as JSON, so nothing that leaves the loop
// shares memory with it.
func (d *Daemon) snapshot() json.RawMessage {
	raw, err := json.Marshal(d.st)
	if err != nil {
		// State holds only plain data; this cannot fail short of a bug.
		panic(fmt.Sprintf("daemon: state does not marshal: %v", err))
	}
	return raw
}
