package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"strelka/internal/clash"
	"strelka/internal/confgen"
	"strelka/internal/engine"
	"strelka/internal/profile"
	"strelka/internal/stats"
	"strelka/internal/store"
	"strelka/internal/xray"
	"strelka/proto"
)

// testWorkers is how many servers are tested at once. Each test is one
// connection to one server, so this is not a load on anything; the limit
// keeps a list of a few hundred from opening them all in the same second,
// and keeps the order: the top of the list answers first.
const testWorkers = 16

// testRun is a server test under way. It belongs to the loop; the goroutine
// that runs the test only reports to it through post.
type testRun struct {
	pub     proto.TestRun
	check   map[string]string       // server id -> queued | running | done
	answers map[string]stats.Answer // what the servers tested so far said
	cancel  context.CancelFunc
}

// probeSpec is what the test's own engine is built from, frozen by the loop.
type probeSpec struct {
	servers []confgen.Server
	xray    []xray.Server
}

// ---- server.test and server.testStop ------------------------------------

func (d *Daemon) serverTest(c rpcCall) (any, *proto.Error) {
	var p proto.ServerTestParams
	if len(c.params) > 0 {
		if err := params(c, &p); err != nil {
			return nil, err
		}
	}
	if err := d.jobBusy(); err != nil {
		return nil, err
	}
	pool, perr := d.testPool(p.IDs)
	if perr != nil {
		return nil, perr
	}

	var api *clash.Client
	var spec *probeSpec
	switch d.st.VPN.Phase {
	case proto.PhaseOn:
		if api = d.api.Load(); api == nil || !d.live.Load() {
			return nil, proto.Errorf(proto.CodeBusy, "VPN ещё не готов. Подожди пару секунд и попробуй снова.")
		}
	case proto.PhaseOff, proto.PhaseFailed:
		// Nothing of the VPN is running to ask: the test brings its own
		// engine, which takes no traffic and leaves the network alone.
		var err error
		if spec, err = d.probeSpec(pool); err != nil {
			d.opts.Logf("server test: %v", err)
			return nil, proto.Errorf(proto.CodeEngine, "Не получилось подготовить проверку: %v.", err)
		}
	case proto.PhaseStarting, proto.PhaseRecovering:
		return nil, proto.Errorf(proto.CodeBusy, "VPN сейчас включается. Проверь серверы, когда он заработает.")
	default:
		return nil, proto.Errorf(proto.CodeBusy, "VPN сейчас выключается. Подожди пару секунд и попробуй снова.")
	}

	ctx, cancel := context.WithCancel(context.Background())
	run := &testRun{
		pub: proto.TestRun{
			Started: time.Now().UTC().Truncate(time.Second),
			Stage:   proto.TestTesting,
			Own:     spec != nil,
			Total:   len(pool),
		},
		check:   make(map[string]string, len(pool)),
		answers: make(map[string]stats.Answer, len(pool)),
		cancel:  cancel,
	}
	if spec != nil {
		run.pub.Stage = proto.TestPreparing
	}
	for _, id := range pool {
		run.check[id] = proto.CheckQueued
	}
	d.test = run
	d.setJob(proto.BusyTesting)
	d.refresh()
	d.touch()

	d.tests.Add(1)
	go func() {
		defer d.tests.Done()
		d.runTest(ctx, run, pool, api, spec)
	}()
	return nil, nil
}

func (d *Daemon) serverTestStop() (any, *proto.Error) {
	if d.test != nil {
		d.endTest(d.test, proto.LevelInfo, "Проверка серверов остановлена.")
	}
	if d.speed != nil {
		d.endSpeed(d.speed, proto.LevelInfo, "Замер скорости остановлен.")
	}
	return nil, nil
}

// testPool is the servers to test, in the order to test them: the ids as
// given, or every server in the order the window lists them.
func (d *Daemon) testPool(ids []string) ([]string, *proto.Error) {
	if len(ids) == 0 {
		pool := listOrder(d.prof)
		if len(pool) == 0 {
			return nil, proto.Errorf(proto.CodeNoServers, "Нет ни одного сервера. Сначала добавь подписку.")
		}
		return pool, nil
	}
	seen := make(map[string]bool, len(ids))
	pool := make([]string, 0, len(ids))
	for _, id := range ids {
		if d.prof.Server(id) == nil {
			return nil, proto.Errorf(proto.CodeNotFound, "Нет такого сервера: %s.", id)
		}
		if !seen[id] {
			seen[id] = true
			pool = append(pool, id)
		}
	}
	return pool, nil
}

// listOrder is every server the way the window groups them: favourites in
// their order, each subscription in turn, then servers added by link.
func listOrder(p *profile.Profile) []string {
	out := make([]string, 0, len(p.Servers))
	taken := make(map[string]bool, len(p.Servers))
	add := func(id string) {
		if !taken[id] {
			taken[id] = true
			out = append(out, id)
		}
	}
	for _, id := range p.Favorites {
		if p.Server(id) != nil {
			add(id)
		}
	}
	for _, sub := range p.Subs {
		for _, s := range p.Servers {
			if s.Sub == sub.ID {
				add(s.ID)
			}
		}
	}
	for _, s := range p.Servers {
		add(s.ID) // by link, or of a subscription the profile no longer lists
	}
	return out
}

// interruptTest ends a test, of servers or of speed, whose engine is going
// away. A test on its own engine survives the VPN's engine stopping, but not
// the VPN starting: the new tunnel would take its connections and make the
// numbers about the VPN's server rather than each server's own.
func (d *Daemon) interruptTest(starting bool, why string) {
	if d.test != nil && (!d.test.pub.Own || starting) {
		d.endTest(d.test, proto.LevelInfo, "Проверка серверов прервана: "+why+".")
	}
	if d.speed != nil && (!d.speed.pub.Own || starting) {
		d.endSpeed(d.speed, proto.LevelInfo, "Замер скорости прерван: "+why+".")
	}
}

// ---- the loop's side of a run --------------------------------------------

func (d *Daemon) testStage(run *testRun, stage string) {
	if d.test != run {
		return
	}
	run.pub.Stage = stage
	d.refresh()
	d.touch()
}

func (d *Daemon) testRunning(run *testRun, id string) {
	if d.test != run {
		return
	}
	run.check[id] = proto.CheckRunning
	d.refresh()
	d.touch()
}

func (d *Daemon) testAnswer(run *testRun, id string, ms int, ok bool, at time.Time) {
	if d.test != run {
		return
	}
	run.check[id] = proto.CheckDone
	run.answers[id] = stats.Answer{OK: ok, Ms: ms, At: at}
	run.pub.Done++
	if ok {
		run.pub.Alive++
		d.pings[id] = ping{ms: ms, at: at, alive: true}
	} else {
		d.pings[id] = ping{at: at}
	}
	d.refresh()
	d.touch()
}

// endTest closes a run, whole or cut short. what leads the journal line of a
// run cut short; "" is a run that got through every server.
func (d *Daemon) endTest(run *testRun, level, what string) {
	if d.test != run {
		return
	}
	run.cancel()
	d.test = nil
	now := time.Now()
	if d.stats.Round(now, run.answers) {
		d.saveStats(now)
	}
	p := run.pub
	switch {
	case what == "":
		d.log(proto.CatServer, proto.LevelInfo, fmt.Sprintf("Проверка серверов: отвечают %d из %d.", p.Alive, p.Total))
	case p.Done == 0:
		d.log(proto.CatServer, level, what)
	default:
		d.log(proto.CatServer, level, fmt.Sprintf("%s Проверено %d из %d, отвечают %d.", what, p.Done, p.Total, p.Alive))
	}
	d.setJob("")
	d.refresh()
	d.touch()
}

// ---- the run itself, outside the loop -----------------------------------

func (d *Daemon) runTest(ctx context.Context, run *testRun, pool []string, api *clash.Client, spec *probeSpec) {
	if spec != nil {
		pe, err := d.startProbe(ctx, spec, nil, func() {
			d.post(func() {
				d.endTest(run, proto.LevelProblem, "Проверка серверов прервана: проверочный движок остановился.")
			})
		})
		if err != nil {
			if ctx.Err() == nil {
				d.opts.Logf("server test: own engine: %v", err)
				d.post(func() {
					d.endTest(run, proto.LevelProblem, "Проверка серверов не удалась: не запустился проверочный движок.")
				})
			}
			return
		}
		defer pe.close()
		api = pe.api
		d.post(func() { d.testStage(run, proto.TestTesting) })
	}

	jobs := make(chan string)
	var wg sync.WaitGroup
	for range min(testWorkers, len(pool)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				d.post(func() { d.testRunning(run, id) })
				// Each server gets its own ProbeTimeout: one slow server
				// costs one worker a few seconds, not the rest of the list.
				ms, err := api.Delay(ctx, id)
				if ctx.Err() != nil {
					return // stopped: what was in flight says nothing
				}
				if err != nil && clash.Unreachable(err) {
					d.opts.Logf("server test: the engine is gone: %v", err)
					d.post(func() {
						d.endTest(run, proto.LevelProblem, "Проверка серверов прервана: движок перестал отвечать.")
					})
					return
				}
				at := time.Now()
				d.post(func() { d.testAnswer(run, id, ms, err == nil, at) })
			}
		}()
	}
feed:
	for _, id := range pool {
		select {
		case jobs <- id:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() == nil {
		d.post(func() { d.endTest(run, proto.LevelInfo, "") })
	}
}

// ---- the test's own engine ----------------------------------------------

// probeSpec freezes the servers of a test for its own engine. Xray servers
// get ports of a range of their own, see xray.ProbeBasePort.
func (d *Daemon) probeSpec(pool []string) (*probeSpec, error) {
	var tags []string
	for _, id := range pool {
		if d.prof.Server(id).Via == proto.ViaXray {
			tags = append(tags, id)
		}
	}
	free := d.opts.PortFree
	if free == nil {
		free = xray.PortFree
	}
	ports, err := xray.AssignPortsFrom(xray.ProbeBasePort, tags, nil, free)
	if err != nil {
		return nil, err
	}
	spec := &probeSpec{}
	for _, id := range pool {
		s := d.prof.Server(id)
		cs := confgen.Server{ID: id, Outbound: s.Outbound}
		if s.Via == proto.ViaXray {
			cs.XrayPort = ports[id]
			spec.xray = append(spec.xray, xray.Server{Tag: id, Port: cs.XrayPort, Outbound: s.Xray})
		}
		spec.servers = append(spec.servers, cs)
	}
	return spec, nil
}

// probeEngine is a sing-box, and an Xray when some server needs one, that
// live for one test.
type probeEngine struct {
	api     *clash.Client
	sups    []*engine.Supervisor
	files   []string
	closing atomic.Bool
	stop    chan struct{} // closed by close: the watcher of the engines goes
}

// probePolicy gives up on the first crash: a test has nobody to wait for a
// restart, and a crash is better reported than retried.
var probePolicy = engine.Policy{Delays: []time.Duration{time.Hour}, CrashLimit: 1, CrashWindow: time.Hour}

// startProbe brings the test's own engines up and returns once they serve.
// A speed test gives it a gate to serve too. died is called if one of them
// stops on its own afterwards.
func (d *Daemon) startProbe(ctx context.Context, spec *probeSpec, g *confgen.Gate, died func()) (*probeEngine, error) {
	port, err := engine.FreePort()
	if err != nil {
		return nil, err
	}
	secret, err := engine.NewSecret()
	if err != nil {
		return nil, err
	}
	controller := fmt.Sprintf("127.0.0.1:%d", port)
	raw, err := confgen.BuildProbe(spec.servers, controller, secret, g)
	if err != nil {
		return nil, err
	}
	pe := &probeEngine{api: clash.New(controller, secret), stop: make(chan struct{})}
	// Next to the VPN's own configs, under names of their own: both may be
	// in use at the same moment.
	cfgPath := scratch(d.opts.Paths.RunConfig(), "probe")
	if err := store.WriteAtomic(cfgPath, raw, 0o600); err != nil {
		return nil, err
	}
	pe.files = append(pe.files, cfgPath)

	events := make(chan engine.Event, 32)
	want := 1
	pe.sups = append(pe.sups, d.probeSupervisor(boxName, events, func() (engine.Launch, func(context.Context) error, error) {
		l := engine.Launch{
			Exe:  d.opts.Paths.Engine(boxName),
			Args: []string{"run", "-c", cfgPath, "-D", d.opts.Paths.Data, "--disable-color"},
			Dir:  d.opts.Paths.Data,
		}
		if d.opts.Launch != nil {
			l = d.opts.Launch(cfgPath)
		}
		return l, func(ctx context.Context) error { _, err := pe.api.Version(ctx); return err }, nil
	}))
	if len(spec.xray) > 0 {
		xraw, err := xray.Build(spec.xray)
		if err != nil {
			pe.close()
			return nil, err
		}
		xpath := scratch(d.opts.Paths.XrayConfig(), "probe")
		if err := store.WriteAtomic(xpath, xraw, 0o600); err != nil {
			pe.close()
			return nil, err
		}
		pe.files = append(pe.files, xpath)
		first := spec.xray[0].Port
		want++
		pe.sups = append(pe.sups, d.probeSupervisor(xrayName, events, func() (engine.Launch, func(context.Context) error, error) {
			l := engine.Launch{Exe: d.opts.Paths.Engine(xrayName), Args: []string{"run", "-c", xpath}, Dir: d.opts.Paths.Data}
			if d.opts.LaunchXray != nil {
				l = d.opts.LaunchXray(xpath, first)
			}
			addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(first))
			return l, func(ctx context.Context) error {
				c, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", addr)
				if err != nil {
					return err
				}
				return c.Close()
			}, nil
		}))
	}
	for _, s := range pe.sups {
		s.Start()
	}

	for ready := 0; ready < want; {
		select {
		case ev := <-events:
			switch ev.Kind {
			case engine.Ready:
				ready++
			case engine.Exited, engine.Failed:
				pe.close()
				why := strings.Join(lastLines(ev.Tail, 3), " | ")
				if ev.Err != nil {
					why = ev.Err.Error()
				}
				return nil, errors.New("an engine stopped before it was ready: " + why)
			}
		case <-ctx.Done():
			pe.close()
			return nil, ctx.Err()
		}
	}
	go func() {
		for {
			select {
			case ev := <-events:
				if pe.closing.Load() {
					return
				}
				if ev.Kind == engine.Exited || ev.Kind == engine.Failed {
					died()
					return
				}
			case <-pe.stop:
				return
			}
		}
	}()
	return pe, nil
}

func (d *Daemon) probeSupervisor(name string, events chan<- engine.Event, prepare func() (engine.Launch, func(context.Context) error, error)) *engine.Supervisor {
	return engine.New(engine.Config{
		Name:         name + " (test)",
		Prepare:      prepare,
		LogFile:      d.opts.Paths.EngineLog(name + "-test"),
		Policy:       probePolicy,
		ReadyTimeout: d.opts.ReadyTimeout,
		StopGrace:    d.opts.StopGrace,
		OnEvent: func(ev engine.Event) {
			select {
			case events <- ev:
			default: // nobody listens any more
			}
		},
	})
}

// close stops the engines and removes their configs. It waits for the
// processes to be gone, so it runs outside the loop.
func (pe *probeEngine) close() {
	if pe.closing.Swap(true) {
		return
	}
	close(pe.stop)
	for _, s := range pe.sups {
		s.Close()
	}
	pe.api.Close()
	for _, f := range pe.files {
		os.Remove(f)
	}
}
