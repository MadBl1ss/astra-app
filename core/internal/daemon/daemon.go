// Package daemon is the orchestrator: one loop owns the State document.
// Commands from clients and observations from subsystems come in, a new
// State goes out. Nothing else reads or writes the state, so there are no
// locks around it and the logic can be followed one message at a time.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"strelka/internal/clash"
	"strelka/internal/confgen"
	"strelka/internal/engine"
	"strelka/internal/geo"
	"strelka/internal/guard"
	"strelka/internal/health"
	"strelka/internal/incident"
	"strelka/internal/ipc"
	"strelka/internal/journal"
	"strelka/internal/legacyfind"
	"strelka/internal/netmon"
	"strelka/internal/profile"
	"strelka/internal/rules"
	"strelka/internal/speed"
	"strelka/internal/stats"
	"strelka/internal/store"
	"strelka/proto"
)

// Version of the daemon, reported in hello and in State. A variable so that
// scripts/build-installer.ps1 can stamp the app's version into it
// (-ldflags -X); the window and the service of one install share a number.
var Version = "0.1.0"

// minStateInterval caps the broadcast at ten documents a second; changes
// that come faster are merged into the next one.
const minStateInterval = 100 * time.Millisecond

const (
	boxName  = "sing-box"
	xrayName = "xray"
)

// Options configure a daemon. The zero value of every optional field is the
// production behaviour; tests override the engine commands and the timings.
type Options struct {
	Paths store.Paths

	// NoTUN builds configs without the TUN, with a local proxy instead: the
	// development mode that needs no admin rights and leaves a VPN already
	// running on the machine alone.
	NoTUN bool

	// Launch and LaunchXray override how the engines are started for a
	// prepared config. port is the first local port Xray listens on.
	Launch     func(configPath string) engine.Launch
	LaunchXray func(configPath string, port int) engine.Launch

	// CheckConfig and CheckXray vet a config before it is used; nil runs
	// the engines' own checks.
	CheckConfig func(ctx context.Context, path string) error
	CheckXray   func(ctx context.Context, path string) error
	// PortFree tells whether a local port can be given to Xray.
	PortFree func(port int) bool

	Policy       engine.Policy
	ReadyTimeout time.Duration
	StopGrace    time.Duration

	// Health overrides the default thresholds (health.Default), and HealthTick how
	// often the automata are given the time (once a second). Tests shrink
	// both to run an outage in a moment.
	Health     *health.Tuning
	HealthTick time.Duration
	// WatchNetwork replaces the route monitor: it is given the function to
	// report the default routes with and returns how to stop. Without it the
	// daemon also reads the routes once before its first config.
	WatchNetwork func(report func(netmon.State)) (stop func())

	// Guard is the kill switch. Nil means none - and it is nil unless the
	// caller says otherwise, so that nothing but cmd/strelkad ever reaches
	// for the system's firewall: a test run from an administrator's console
	// must not be able to cut the machine off.
	Guard guard.Guard
	// FindTunnel looks the tunnel adapter up for the guard's permit; nil
	// asks the system for the adapter named confgen.TUNName.
	FindTunnel func() (luid uint64, ok bool)

	// Locator finds where the computer is, for the window's globe. Nil means
	// never, and like Guard it is nil unless the caller says otherwise: only
	// cmd/strelkad asks a service on the internet, a test never does.
	Locator *geo.Locator

	// Fetch and Lookup are how the connection check (diag.run) reaches the
	// internet by itself, when the engine is not there to ask: an HTTP GET
	// past any proxy, and a name lookup through the system. Nil means the
	// real ones; tests put stand-ins here so as not to touch the network.
	Fetch  func(ctx context.Context, url string) error
	Lookup func(ctx context.Context, host string) error

	// SpeedOpen opens a speed test's download through a server; nil goes
	// through the engine's gate to the internet (speed.URLs). SpeedLimits
	// bound one download; zero means speed.Default. Tests put stand-ins
	// here, so as not to touch the network or wait ten seconds a server.
	SpeedOpen   func(ctx context.Context, server string) (io.ReadCloser, error)
	SpeedLimits speed.Limits

	// RateEvery and RateSettle override how often the quiet rounds of the
	// rating run and how long after the VPN comes on the first one waits.
	RateEvery  time.Duration
	RateSettle time.Duration

	// FindLegacy and RetireLegacy find and turn off an old Strelka
	// (legacy.detect, legacy.retire). Nil means the real ones; tests put
	// stand-ins here, since the real retire on a developer's machine would
	// turn off the VPN they are working through.
	FindLegacy   func(ctx context.Context, ours ...string) []legacyfind.Folder
	RetireLegacy func(ctx context.Context, dirs []string) (stopped int, task string, err error)

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

// engEvent is a supervisor's observation, labelled with the engine it is about.
type engEvent struct {
	name string
	ev   engine.Event
}

// loopFunc is work a goroutine hands back to the loop: the only way anything
// outside the loop touches the state.
type loopFunc func()

// ping is the last delay test of a server.
type ping struct {
	ms    int
	at    time.Time
	alive bool
}

// Daemon is one running service.
type Daemon struct {
	opts Options
	srv  *ipc.Server
	sup  *engine.Supervisor // sing-box
	xsup *engine.Supervisor // the Xray helper

	inbox chan any      // rpcCall, engEvent and loopFunc
	done  chan struct{} // closed when the loop has exited

	// Shared with the supervisors' goroutines and the poller.
	plan        atomic.Pointer[plan]         // what the next engine start is built from
	api         atomic.Pointer[clash.Client] // the running sing-box's Clash API
	gate        atomic.Pointer[confgen.Gate] // and its gate for speed tests
	live        atomic.Bool                  // phase is "on": the API may be asked
	checkedBox  atomic.Uint64                // plan generation whose config passed the check
	checkedXray atomic.Uint64
	noV6        atomic.Bool  // the network has no IPv6 way out, see ipv6Mode
	downRate    atomic.Int64 // bytes a second coming in through the engine, by the poller

	// Everything below belongs to the loop goroutine.
	st         proto.State
	journal    *journal.Journal
	dirty      bool
	lastSent   time.Time
	flush      *time.Timer
	flushArmed bool
	everOn     bool // the engine was ready at least once since the last start command

	prof    *profile.Profile
	rules   rules.Set
	stats   *stats.Stats
	pings   map[string]ping
	planGen uint64

	boxReady   bool
	xrayReady  bool
	xrayWanted bool // the current plan has servers that go through Xray

	phaseBusy string         // starting | stopping
	job       string         // updating_subs | testing
	test      *testRun       // the server test under way, see servertest.go
	speed     *speedRun      // the speed test under way, see speedtest.go
	tests     sync.WaitGroup // their goroutines, waited for on the way out
	autoInfo  map[string]autoListInfo
	versions  map[string]versionInfo // engine.versions, by engine name

	// Health and incidents, see health.go.
	tune        health.Tuning
	hm          *health.Machine
	im          *incident.Manager
	hserver     string    // the server the machine was last told about
	lastTick    time.Time // wall clock of the previous tick
	toldOn      bool      // "подключён" was said for this start
	subsChecked time.Time
	statsDirty  bool
	statsSaved  time.Time
	hints       hintGate               // shared with the supervisors' goroutines
	held        map[string]proto.Toast // toasts waiting for somebody to show them

	gs guardState // the kill switch, see guard.go
	ps placeState // where the computer is, see place.go

	diagBusy bool // a diag.run is under way, see diagrun.go

	rate rateState // the quiet rounds of the rating, see rating.go
}

// New prepares a daemon: folders, the saved state, the profile, the rules.
// It also clears engine processes a previous daemon may have left behind.
// The caller should hold the channel's listener by now, so that a second
// daemon fails on the channel before it gets to kill the first one's engine.
func New(opts Options) (*Daemon, error) {
	if opts.FindLegacy == nil {
		opts.FindLegacy = legacyfind.FindAll
	}
	if opts.RetireLegacy == nil {
		opts.RetireLegacy = legacyfind.Retire
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	if err := opts.Paths.Ensure(); err != nil {
		return nil, fmt.Errorf("data folder: %w", err)
	}
	if opts.HealthTick <= 0 {
		opts.HealthTick = time.Second
	}
	d := &Daemon{
		opts:     opts,
		inbox:    make(chan any, 256),
		done:     make(chan struct{}),
		st:       proto.NewState(Version),
		pings:    map[string]ping{},
		autoInfo: map[string]autoListInfo{},
		versions: map[string]versionInfo{},
		tune:     health.Default(),
		held:     map[string]proto.Toast{},
	}
	if opts.Health != nil {
		d.tune = *opts.Health
	}
	d.hm, d.im = health.New(d.tune), incident.New(d.tune)

	saved, err := store.LoadJSON(opts.Paths.DesiredFile(), desiredFile{Desired: proto.DesiredOff})
	if err != nil {
		// A broken file must not keep the daemon down; off is the safe read.
		opts.Logf("desired state unreadable, assuming off: %v", err)
	}
	if saved.Desired == proto.DesiredOn {
		d.st.VPN.Desired = proto.DesiredOn
	}

	settings, err := store.LoadJSON(opts.Paths.SettingsFile(), proto.DefaultSettings())
	if err != nil {
		opts.Logf("settings unreadable, using defaults: %v", err)
	}
	d.st.Settings = cleanSettings(settings)
	d.st.Guard.Enabled, d.st.Guard.AllowLAN = d.st.Settings.Guard, d.st.Settings.AllowLAN

	if d.prof, err = profile.Load(opts.Paths.ProfileFile()); err != nil {
		// The file holds the user's subscriptions: never overwrite one we
		// could not read. Set it aside and start empty.
		aside := opts.Paths.ProfileFile() + ".broken"
		opts.Logf("profile unreadable (%v), moved to %s", err, aside)
		os.Rename(opts.Paths.ProfileFile(), aside)
	}
	if d.rules, err = rules.Ensure(opts.Paths.RulesDir()); err != nil {
		return nil, fmt.Errorf("rules folder: %w", err)
	}
	if err := confgen.WriteAutoLists(opts.Paths.RulesDir()); err != nil {
		return nil, fmt.Errorf("rules folder: %w", err)
	}
	d.stats = stats.Load(opts.Paths.StatsFile())
	d.loadPlace()

	for _, name := range []string{boxName, xrayName} {
		exe := opts.Paths.Engine(name)
		if n, err := engine.KillByPath(exe); err != nil {
			opts.Logf("looking for stray %s: %v", name, err)
		} else if n > 0 {
			opts.Logf("ended %d stray %s left by a previous run (%s)", n, name, exe)
		}
	}

	d.journal = journal.Open(opts.Paths.Journal())
	// Incident ids go on from the last one on record, and warnings already
	// given about a subscription are not given again by a new run.
	d.im.SeedID(d.journal.LastIncident())
	if warned, err := store.LoadJSON(opts.Paths.WarnedFile(), map[string]string{}); err == nil {
		d.im.SetWarned(warned)
	}
	if hidden, err := store.LoadJSON(opts.Paths.HiddenFile(), map[string]string{}); err == nil {
		d.im.SetHidden(hidden)
	}
	d.im.Mute(!d.st.Settings.SubWarnings, !d.st.Settings.ServerWarnings)
	d.srv = ipc.NewServer(d.handle)
	d.sup = d.supervise(boxName, d.prepareBox)
	d.xsup = d.supervise(xrayName, d.prepareXray)

	if opts.WatchNetwork == nil {
		// The first config already knows whether the network has IPv6;
		// otherwise the monitor's first report would restart the engine
		// that has only just been started.
		if st, err := netmon.Now([]string{confgen.TUNName}); err == nil && st.Up() {
			d.noV6.Store(!st.V6)
		}
	}

	d.prof.FixSelection()
	d.replan()
	d.refresh()
	return d, nil
}

func (d *Daemon) supervise(name string, prepare func() (engine.Launch, func(context.Context) error, error)) *engine.Supervisor {
	return engine.New(engine.Config{
		Name:         name,
		Prepare:      prepare,
		LogFile:      d.opts.Paths.EngineLog(name),
		Policy:       d.opts.Policy,
		ReadyTimeout: d.opts.ReadyTimeout,
		StopGrace:    d.opts.StopGrace,
		OnEvent: func(ev engine.Event) {
			select {
			case d.inbox <- engEvent{name: name, ev: ev}:
			case <-d.done:
			}
		},
		OnLine: func(line string) {
			d.srv.Publish(proto.TopicLog, proto.LogLine{
				T: time.Now().UTC(), Source: name, Level: engine.Level(line), Line: line,
			})
			d.onLine(name, line)
		},
	})
}

// post hands work to the loop from another goroutine.
func (d *Daemon) post(fn func()) {
	select {
	case d.inbox <- loopFunc(fn):
	case <-d.done:
	}
}

// Run serves clients on l and runs the loop until ctx is cancelled, then
// stops the engines and returns. The desired state is not touched on the
// way out: the next run picks up where this one left.
func (d *Daemon) Run(ctx context.Context, l net.Listener) error {
	served := make(chan error, 1)
	go func() { served <- d.srv.Serve(l) }()
	go d.pollEngine()
	stopNetwork := d.watchNetwork()

	d.loop(ctx)
	stopNetwork()
	// A server test on its own engine stops that engine on its way out.
	waitTests := make(chan struct{})
	go func() { d.tests.Wait(); close(waitTests) }()
	select {
	case <-waitTests:
	case <-time.After(10 * time.Second):
		d.opts.Logf("shutting down: the server test did not stop its engine")
	}

	// Let the last State (phase off) reach the clients before they are cut.
	d.srv.Drain(500 * time.Millisecond)
	l.Close()
	d.srv.Close()
	d.sup.Close()
	d.xsup.Close()
	// Last, as on every stop: the engine first, the guard after it.
	d.releaseGuard()
	if d.statsDirty {
		d.saveStats(time.Now())
	}
	d.journal.Close()
	return <-served
}

func (d *Daemon) loop(ctx context.Context) {
	defer close(d.done)
	d.flush = time.NewTimer(time.Hour)
	d.flush.Stop()

	d.log(proto.CatSystem, proto.LevelInfo, "Служба запущена.")
	if d.st.VPN.Desired == proto.DesiredOn && d.st.Settings.StartOnBoot {
		if len(d.prof.Servers) == 0 {
			d.setDesired(proto.DesiredOff)
			d.log(proto.CatVPN, proto.LevelProblem, "VPN не включён: нет ни одного сервера. Добавь подписку.")
		} else {
			d.beginStart()
		}
	}
	d.checkSubs()
	// Right away rather than with the first minute: on a first run the
	// globe would otherwise start from the time zone's guess.
	d.autoPlace(time.Now())
	d.touch()

	tick := time.NewTicker(d.opts.HealthTick)
	defer tick.Stop()

	// Subscriptions refresh by themselves. The first look comes soon after
	// the start, so a daemon that was off for a day catches up.
	auto := time.NewTimer(15 * time.Second)
	defer auto.Stop()

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
				result, err, later := d.dispatch(m)
				if !later {
					m.reply <- rpcReply{result: result, err: err}
				}
			case engEvent:
				d.onEngine(m)
			case loopFunc:
				m()
			}
			d.syncGuard(time.Now(), false)
			if stopping == nil && d.st.VPN.Phase == proto.PhaseOff {
				d.shutdown()
				return
			}
		case <-d.flush.C:
			d.flushArmed = false
			d.publish()
		case <-tick.C:
			d.tick()
		case <-auto.C:
			auto.Reset(time.Minute)
			if stopping != nil {
				d.autoUpdate()
				d.autoPlace(time.Now())
			}
		case <-stopping:
			stopping = nil // handled once
			if d.st.VPN.Phase == proto.PhaseOff {
				d.shutdown()
				return
			}
			d.opts.Logf("shutting down: stopping the engines")
			d.setPhase(proto.PhaseStopping)
			d.setBusy(proto.BusyStopping)
			d.touch()
			d.stopEngines()
			// The supervisor kills after its own grace; this is the belt to
			// those braces, so a bug there cannot hang the shutdown.
			giveUp = time.After(15 * time.Second)
		case <-giveUp:
			d.opts.Logf("shutting down: %s did not confirm the stop", boxName)
			d.shutdown()
			return
		}
	}
}

func (d *Daemon) shutdown() {
	d.live.Store(false)
	if d.test != nil {
		d.test.cancel()
	}
	if d.speed != nil {
		d.speed.cancel()
	}
	d.log(proto.CatSystem, proto.LevelInfo, "Служба остановлена.")
	d.publish()
}

// handle is the ipc handler. It runs in a connection's read loop and only
// hands the call to the loop, so calls reach the loop in the order they
// were sent; the answer is waited for outside the read loop.
func (d *Daemon) handle(c *ipc.Conn, method string, params json.RawMessage) func() (any, *proto.Error) {
	call := rpcCall{conn: c, method: method, params: params, reply: make(chan rpcReply, 1)}
	gone := func() (any, *proto.Error) {
		return nil, proto.Errorf(proto.CodeBusy, "Служба завершает работу.")
	}
	select {
	case d.inbox <- call:
	case <-d.done:
		return gone
	}
	return func() (any, *proto.Error) {
		select {
		case r := <-call.reply:
			return r.result, r.err
		case <-d.done:
			return gone()
		}
	}
}

// dispatch answers one call. later is true when the answer will come from
// a job that is still running; the handler then owns c.reply.
func (d *Daemon) dispatch(c rpcCall) (result any, err *proto.Error, later bool) {
	switch c.method {
	case proto.MethodHello:
		return proto.HelloResult{Version: Version, Proto: proto.Version, OS: runtime.GOOS}, nil, false
	case proto.MethodStateGet:
		return d.snapshot(), nil, false
	case proto.MethodStateSubscribe, proto.MethodStateUnsubscribe:
		result, err = d.subscription(c)
	case proto.MethodVPNStart:
		result, err = d.vpnStart()
	case proto.MethodVPNStop:
		result, err = d.vpnStop()
	case proto.MethodVPNRestart:
		result, err = d.vpnRestart()
	case proto.MethodEngineStatus:
		result = d.engineStatus()
	case proto.MethodJournalList:
		result, err = d.journalList(c)
	case proto.MethodIncidentAction:
		result, err = d.incidentAction(c)
	case proto.MethodEngineVersions:
		return d.engineVersions(c)
	case proto.MethodDiagExport:
		return d.diagExport(c)
	case proto.MethodGuardStatus:
		result = d.guardStatus()
	case proto.MethodPlaceDetect:
		return d.placeDetect(c)
	case proto.MethodPlaceSet:
		result, err = d.placeSet(c)
	case proto.MethodDiagRun:
		return d.diagRun(c)
	case proto.MethodLegacyDetect:
		return d.legacyDetect(c)
	case proto.MethodLegacyRetire:
		return d.legacyRetire(c)
	default:
		return d.dispatchProfile(c)
	}
	return result, err, false
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
		if t == proto.TopicToast {
			d.deliverHeld(c.conn)
		}
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
	d.acted()
	switch d.st.VPN.Phase {
	case proto.PhaseStopping:
		return nil, proto.Errorf(proto.CodeBusy, "VPN ещё выключается. Подожди пару секунд и попробуй снова.")
	case proto.PhaseStarting, proto.PhaseOn, proto.PhaseRecovering:
		d.setDesired(proto.DesiredOn)
		return nil, nil
	}
	if len(d.prof.Servers) == 0 {
		return nil, proto.Errorf(proto.CodeNoServers, "Нет ни одного сервера. Сначала добавь подписку.")
	}
	d.setDesired(proto.DesiredOn)
	d.beginStart()
	return nil, nil
}

func (d *Daemon) vpnStop() (any, *proto.Error) {
	d.acted()
	d.setDesired(proto.DesiredOff)
	switch d.st.VPN.Phase {
	case proto.PhaseOff, proto.PhaseStopping:
		return nil, nil
	}
	d.setPhase(proto.PhaseStopping)
	d.setBusy(proto.BusyStopping)
	d.touch()
	d.stopEngines()
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
	d.acted()
	d.log(proto.CatVPN, proto.LevelInfo, "Перезапускаю VPN.")
	d.restartEngines()
	return nil, nil
}

// beginStart brings the engines up from a standstill.
func (d *Daemon) beginStart() {
	d.replan()
	d.restartEngines()
}

// restartEngines starts both engines afresh from the current plan. Restart
// on an idle supervisor is a start, and on a running one it guarantees a new
// Ready event, which a plain Start would not give.
func (d *Daemon) restartEngines() {
	d.markStarting()
	// The guard goes up before the engine does: what the engine's start
	// leaves uncovered for a second is the leak this is all about.
	d.syncGuard(time.Now(), true)
	d.sup.Restart()
	if d.xrayWanted {
		d.xsup.Restart()
	} else {
		d.xsup.Stop()
	}
}

func (d *Daemon) stopEngines() {
	d.interruptTest(false, "VPN выключается")
	d.live.Store(false)
	d.sup.Stop()
	d.xsup.Stop()
}

func (d *Daemon) markStarting() {
	if (d.test != nil && d.test.pub.Own) || (d.speed != nil && d.speed.pub.Own) {
		d.interruptTest(true, "включается VPN")
	} else {
		d.interruptTest(true, "VPN перезапускается")
	}
	d.everOn, d.toldOn = false, false
	d.boxReady, d.xrayReady = false, false
	d.live.Store(false)
	d.feed(health.Helper{}) // a fresh start owes nothing to the helper's past
	d.setPhase(proto.PhaseStarting)
	d.setBusy(proto.BusyStarting)
	// Only now: while the phase still said "failed", the reason was still
	// what the open incident is worded with.
	d.st.VPN.FailReason, d.st.VPN.FailText = nil, nil
	d.touch()
}

func (d *Daemon) engineStatus() proto.EngineStatusResult {
	one := func(name string, sup *engine.Supervisor) proto.EngineStatus {
		st := sup.Status()
		es := proto.EngineStatus{
			Name:     name,
			Path:     d.opts.Paths.Engine(name),
			Running:  st.Running,
			PID:      st.PID,
			Restarts: st.Restarts,
		}
		if st.Running {
			since := st.Since.UTC()
			es.Since = &since
		}
		return es
	}
	res := proto.EngineStatusResult{Engines: []proto.EngineStatus{one(boxName, d.sup)}}
	// The helper is listed only while it has work: an idle line for an
	// engine that is not supposed to run reads like a fault.
	if x := one(xrayName, d.xsup); d.xrayWanted || x.Running {
		res.Engines = append(res.Engines, x)
	}
	return res
}

// onEngine applies one observation from a supervisor.
func (d *Daemon) onEngine(m engEvent) {
	if m.name == xrayName {
		d.onXray(m.ev)
		return
	}
	ev := m.ev
	switch ev.Kind {
	case engine.Started:
		d.opts.Logf("%s started, pid %d", boxName, ev.PID)
		d.engineUp()

	case engine.Ready:
		d.opts.Logf("%s is ready, pid %d", boxName, ev.PID)
		d.boxReady = true
		d.maybeOn()

	case engine.Exited:
		d.opts.Logf("%s exited: code %d, uptime %v, not ready %v, retry in %v",
			boxName, ev.ExitCode, ev.Uptime.Round(time.Millisecond), ev.NotReady, ev.RetryIn)
		for _, line := range lastLines(ev.Tail, 5) {
			d.opts.Logf("    %s", line)
		}
		d.boxReady = false
		d.live.Store(false)
		d.interruptTest(false, "VPN неожиданно остановился")
		d.engineDown()
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
			d.opts.Logf("%s stopped %s, pid %d", boxName, how, ev.PID)
		}
		d.boxReady = false
		d.engineDown()
		// In "failed" the engine was stopped because the helper gave up;
		// the failure, not this stop, is what the user must see.
		if d.st.VPN.Phase == proto.PhaseOff || d.st.VPN.Phase == proto.PhaseFailed {
			return
		}
		d.setPhase(proto.PhaseOff)
		d.setBusy("")
		d.st.VPN.FailReason, d.st.VPN.FailText = nil, nil
		d.log(proto.CatVPN, proto.LevelInfo, "VPN выключен.")
		d.touch()

	case engine.Failed:
		d.opts.Logf("%s failed: %s: %v", boxName, ev.Reason, ev.Err)
		d.boxReady = false
		d.engineDown()
		d.xsup.Stop()
		if d.st.VPN.Phase == proto.PhaseStopping {
			// Giving up is as good as stopped for someone who asked to stop.
			d.setPhase(proto.PhaseOff)
			d.setBusy("")
			d.log(proto.CatVPN, proto.LevelInfo, "VPN выключен.")
			d.touch()
			return
		}
		reason := ev.Reason
		if reason == engine.FailCrashLoop {
			// The engine's last words often say why it keeps dying.
			if why := health.FailReason(d.sup.Tail(40)); why != "" {
				reason = why
			}
		}
		d.fail(reason, failText(reason))
	}
}

// onXray applies an observation about the helper. sing-box is the VPN; the
// helper only matters for the servers that go through it, so its trouble
// does not take the phase down once the VPN is on.
func (d *Daemon) onXray(ev engine.Event) {
	phase := d.st.VPN.Phase
	switch ev.Kind {
	case engine.Started:
		d.opts.Logf("%s started, pid %d", xrayName, ev.PID)

	case engine.Ready:
		d.opts.Logf("%s is ready, pid %d", xrayName, ev.PID)
		d.xrayReady = true
		if phase == proto.PhaseOn {
			d.log(proto.CatVPN, proto.LevelInfo, "Помощник Xray снова работает.")
			d.feed(health.Helper{})
			d.touch()
			return
		}
		d.maybeOn()

	case engine.Exited:
		d.opts.Logf("%s exited: code %d, uptime %v, retry in %v", xrayName, ev.ExitCode, ev.Uptime.Round(time.Millisecond), ev.RetryIn)
		for _, line := range lastLines(ev.Tail, 5) {
			d.opts.Logf("    %s", line)
		}
		d.xrayReady = false
		if phase == proto.PhaseStopping || phase == proto.PhaseOff || phase == proto.PhaseFailed {
			return
		}
		what := "Помощник Xray неожиданно остановился."
		if ev.RetryIn > 0 {
			what += fmt.Sprintf(" Перезапускаю через %s.", russianDelay(ev.RetryIn))
		}
		d.log(proto.CatVPN, proto.LevelProblem, what)
		if phase == proto.PhaseOn {
			d.feed(health.Helper{Down: true})
		}
		d.touch()

	case engine.Stopped:
		d.xrayReady = false
		if ev.PID != 0 {
			d.opts.Logf("%s stopped, pid %d, forced %v", xrayName, ev.PID, ev.Forced)
		}

	case engine.Failed:
		d.opts.Logf("%s failed: %s: %v", xrayName, ev.Reason, ev.Err)
		d.xrayReady = false
		if !d.xrayWanted {
			// A start that the next plan made pointless: its stop is queued.
			return
		}
		switch phase {
		case proto.PhaseStarting:
			// The selected server may be one of Xray's; coming up without
			// the helper would be "on" with no traffic.
			d.sup.Stop()
			d.fail(ev.Reason, "Не запускается помощник Xray, через который работает часть серверов. Подробности — в журнале.")
		case proto.PhaseOn, proto.PhaseRecovering:
			d.log(proto.CatVPN, proto.LevelCritical, "Помощник Xray не запускается. Серверы, которые работают через него, недоступны.")
			d.feed(health.Helper{Down: true, Gone: true})
			d.touch()
		}
	}
}

// maybeOn moves to "on" once every engine the plan needs is ready.
func (d *Daemon) maybeOn() {
	if !d.boxReady || (d.xrayWanted && !d.xrayReady) {
		return
	}
	if d.st.VPN.Phase != proto.PhaseStarting && d.st.VPN.Phase != proto.PhaseRecovering {
		return // a stop is already on its way, or the VPN is on already
	}
	text := "VPN включён."
	if d.everOn {
		text = "VPN снова работает."
	}
	d.everOn = true
	d.setPhase(proto.PhaseOn)
	d.setBusy("")
	d.log(proto.CatVPN, proto.LevelInfo, text)
	d.live.Store(true)
	d.syncEngine()
	d.touch()
}

// fail ends a start that did not work out. The journal line and the toast
// come from the incident this opens: the reason is in place before the
// phase moves, so that the incident can word it.
func (d *Daemon) fail(reason, text string) {
	d.live.Store(false)
	d.st.VPN.FailReason, d.st.VPN.FailText = &reason, &text
	d.setPhase(proto.PhaseFailed)
	d.setBusy("")
	d.touch()
}

func failText(reason string) string {
	switch reason {
	case engine.FailMissing:
		return "Не хватает файлов Astra: нет движка sing-box. Переустанови приложение."
	case engine.FailConfig:
		return "Движок не принял настройки VPN. Подробности — в журнале."
	case engine.FailCrashLoop:
		return "VPN падает сразу после запуска. Подробности — в журнале."
	case proto.FailTUN:
		return "Не создаётся сетевой адаптер VPN. Обычно мешает другой включённый VPN или не хватает прав администратора."
	case proto.FailPort:
		return "Движку не достался нужный порт: его занимает другая программа."
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

// setPhase moves the phase and tells the health machine, which makes the
// verdict out of it.
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
	ev := health.Phase{Phase: phase}
	if phase == proto.PhaseFailed && d.st.VPN.FailReason != nil {
		ev.Reason = *d.st.VPN.FailReason
	}
	d.feed(ev)
}

// setBusy records what the phase is busy with; setJob, what a background
// job is. They are kept apart because they end independently: the engine
// becoming ready must not hide that subscriptions are still downloading.
func (d *Daemon) setBusy(busy string) {
	d.phaseBusy = busy
	d.applyBusy()
}

func (d *Daemon) setJob(job string) {
	d.job = job
	d.applyBusy()
	d.touch()
}

func (d *Daemon) applyBusy() {
	busy := d.phaseBusy
	if busy == "" {
		busy = d.job
	}
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

func (d *Daemon) log(cat, level, text string) { d.logFor("", cat, level, text) }

// logFor writes a journal entry that belongs to an incident.
func (d *Daemon) logFor(incidentID, cat, level, text string) {
	e := d.journal.Add(time.Now(), cat, level, text, incidentID)
	d.srv.Publish(proto.TopicJournal, e)
}

// touch marks the state changed and makes sure a broadcast is scheduled.
func (d *Daemon) touch() {
	d.st.Rev++
	d.dirty = true
	if d.flushArmed || d.flush == nil {
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
