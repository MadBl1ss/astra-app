package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"strelka/internal/clash"
	"strelka/internal/confgen"
	"strelka/internal/speed"
	"strelka/internal/stats"
	"strelka/proto"
)

// A speed test takes the servers one at a time: two downloads side by side
// would share the user's own line and each would measure half of it. Each
// server is pinged a few times, then made to download for a few seconds
// through the engine's gate (confgen.Gate), past the selected server.
const (
	// speedPings: the middle of three is the ping, their spread the wobble.
	speedPings = 3
	// maxSpeedServers bounds one run: every server costs up to
	// speed.Default.Bytes of the subscription's traffic.
	maxSpeedServers = 12
	// How often the window hears about the download under way.
	speedEvery = 150 * time.Millisecond
	// A download that has to be given up on this early says nothing.
	speedMinBytes = 256 << 10
	// How long the download may take to start: the connection through the
	// server, and the answer of the site.
	speedOpenTimeout = 20 * time.Second
)

// speedRun is a speed test under way. Like testRun it belongs to the loop;
// the goroutine that runs the test reports to it through post.
type speedRun struct {
	pub     proto.SpeedRun
	answers map[string]stats.Answer // the last ping of each server, or its silence
	cancel  context.CancelFunc
}

// measured is how one server did, as the goroutine hands it to the loop.
type measured struct {
	res   proto.SpeedResult
	pings []stats.Answer
	mbps  float64
	at    time.Time
	gone  bool // the engine stopped answering: the run is over
}

func (d *Daemon) speedLimits() speed.Limits {
	if d.opts.SpeedLimits != (speed.Limits{}) {
		return d.opts.SpeedLimits
	}
	return speed.Default
}

// ---- server.speedTest ------------------------------------------------------

func (d *Daemon) serverSpeed(c rpcCall) (any, *proto.Error) {
	var p proto.ServerSpeedParams
	if len(c.params) > 0 {
		if err := params(c, &p); err != nil {
			return nil, err
		}
	}
	if err := d.jobBusy(); err != nil {
		return nil, err
	}
	ids := p.IDs
	if len(ids) == 0 {
		if d.prof.Selected == "" {
			return nil, proto.Errorf(proto.CodeNoServers, "Нет ни одного сервера. Сначала добавь подписку.")
		}
		ids = []string{d.prof.Selected}
	}
	pool, perr := d.testPool(ids)
	if perr != nil {
		return nil, perr
	}
	if len(pool) > maxSpeedServers {
		return nil, proto.Errorf(proto.CodeInvalid, "За один раз можно замерить не больше %d серверов: каждый тратит трафик подписки.", maxSpeedServers)
	}

	var api *clash.Client
	var gate *confgen.Gate
	var spec *probeSpec
	switch d.st.VPN.Phase {
	case proto.PhaseOn:
		api, gate = d.api.Load(), d.gate.Load()
		if api == nil || gate == nil || !d.live.Load() {
			return nil, proto.Errorf(proto.CodeBusy, "VPN ещё не готов. Подожди пару секунд и попробуй снова.")
		}
	case proto.PhaseOff, proto.PhaseFailed:
		var err error
		if spec, err = d.probeSpec(pool); err != nil {
			d.opts.Logf("speed test: %v", err)
			return nil, proto.Errorf(proto.CodeEngine, "Не получилось подготовить замер: %v.", err)
		}
	case proto.PhaseStarting, proto.PhaseRecovering:
		return nil, proto.Errorf(proto.CodeBusy, "VPN сейчас включается. Замерь скорость, когда он заработает.")
	default:
		return nil, proto.Errorf(proto.CodeBusy, "VPN сейчас выключается. Подожди пару секунд и попробуй снова.")
	}

	lim := d.speedLimits()
	ctx, cancel := context.WithCancel(context.Background())
	run := &speedRun{
		pub: proto.SpeedRun{
			Started:    time.Now().UTC().Truncate(time.Second),
			Stage:      proto.TestTesting,
			Own:        spec != nil,
			IDs:        pool,
			Results:    []proto.SpeedResult{},
			MaxBytes:   lim.Bytes,
			MaxSeconds: int(math.Ceil(lim.Time.Seconds())),
		},
		answers: make(map[string]stats.Answer, len(pool)),
		cancel:  cancel,
	}
	if spec != nil {
		run.pub.Stage = proto.TestPreparing
	}
	d.speed = run
	d.setJob(proto.BusyMeasuring)
	d.refresh()
	d.touch()

	d.tests.Add(1)
	go func() {
		defer d.tests.Done()
		d.runSpeed(ctx, run, api, gate, spec, lim)
	}()
	return nil, nil
}

// ---- the loop's side of a run ------------------------------------------------

func (d *Daemon) speedStage(run *speedRun, stage string) {
	if d.speed != run {
		return
	}
	run.pub.Stage = stage
	d.refresh()
	d.touch()
}

func (d *Daemon) speedPhase(run *speedRun, id, phase string) {
	if d.speed != run {
		return
	}
	run.pub.Current, run.pub.Phase = &id, &phase
	d.refresh()
	d.touch()
}

func (d *Daemon) speedResult(run *speedRun, m measured) {
	if d.speed != run {
		return
	}
	id := m.res.ID
	run.pub.Current, run.pub.Phase = nil, nil
	run.pub.Results = append(run.pub.Results, m.res)
	for _, a := range m.pings {
		d.statsDirty = d.stats.Answered(id, a.Ms, a.At) || d.statsDirty
	}
	if n := len(m.pings); n > 0 {
		run.answers[id] = m.pings[n-1]
		d.pings[id] = ping{ms: *m.res.Ping, at: m.pings[n-1].At, alive: true}
	} else {
		run.answers[id] = stats.Answer{}
		d.pings[id] = ping{at: m.at}
	}
	if m.res.Mbps != nil {
		d.statsDirty = d.stats.Speed(id, m.mbps, m.at) || d.statsDirty
	}
	d.refresh()
	d.touch()
}

// endSpeed closes a run, whole or cut short. what leads the journal line of
// a run cut short; "" is a run that got through every server.
func (d *Daemon) endSpeed(run *speedRun, level, what string) {
	if d.speed != run {
		return
	}
	run.cancel()
	d.speed = nil
	now := time.Now()
	// Misses count as in a server test: only when somebody else answered.
	d.stats.Round(now, run.answers)
	if d.statsDirty || len(run.answers) > 0 {
		d.saveStats(now)
	}
	line := what
	if best := bestSpeed(run.pub.Results); best != nil {
		s := fmt.Sprintf("Замер скорости: лучший — %s, %s Мбит/с.", d.serverName(best.ID), mbpsText(*best.Mbps))
		if what != "" {
			s = what + " " + s
		}
		line = s
	} else if what == "" {
		line = "Замер скорости: ни один сервер не скачал файл."
	}
	if line != "" {
		d.log(proto.CatServer, level, line)
	}
	d.setJob("")
	d.refresh()
	d.touch()
}

func bestSpeed(results []proto.SpeedResult) *proto.SpeedResult {
	var best *proto.SpeedResult
	for i := range results {
		r := &results[i]
		if r.Mbps != nil && (best == nil || *r.Mbps > *best.Mbps) {
			best = r
		}
	}
	return best
}

// mbpsText words a speed the Russian way: "84", "9,6".
func mbpsText(mbps float64) string {
	if mbps >= 10 {
		return fmt.Sprintf("%.0f", mbps)
	}
	s := fmt.Sprintf("%.1f", mbps)
	for i := range s {
		if s[i] == '.' {
			return s[:i] + "," + s[i+1:]
		}
	}
	return s
}

// ---- the run itself, outside the loop ------------------------------------------

func (d *Daemon) runSpeed(ctx context.Context, run *speedRun, api *clash.Client, gate *confgen.Gate, spec *probeSpec, lim speed.Limits) {
	if spec != nil {
		var err error
		if gate, err = newGate(); err != nil {
			d.post(func() {
				d.endSpeed(run, proto.LevelProblem, "Замер скорости не удался: не нашлось свободного порта.")
			})
			return
		}
		pe, err := d.startProbe(ctx, spec, gate, func() {
			d.post(func() {
				d.endSpeed(run, proto.LevelProblem, "Замер скорости прерван: проверочный движок остановился.")
			})
		})
		if err != nil {
			if ctx.Err() == nil {
				d.opts.Logf("speed test: own engine: %v", err)
				d.post(func() {
					d.endSpeed(run, proto.LevelProblem, "Замер скорости не удался: не запустился проверочный движок.")
				})
			}
			return
		}
		defer pe.close()
		api = pe.api
		d.post(func() { d.speedStage(run, proto.TestTesting) })
	}

	for _, id := range run.pub.IDs {
		m := d.measure(ctx, run, id, api, gate, lim)
		if ctx.Err() != nil {
			return // stopped: what was in flight says nothing
		}
		if m.gone {
			d.opts.Logf("speed test: the engine is gone")
			d.post(func() {
				d.endSpeed(run, proto.LevelProblem, "Замер скорости прерван: движок перестал отвечать.")
			})
			return
		}
		d.post(func() { d.speedResult(run, m) })
	}
	d.post(func() { d.endSpeed(run, proto.LevelInfo, "") })
}

// measure pings one server and makes it download.
func (d *Daemon) measure(ctx context.Context, run *speedRun, id string, api *clash.Client, gate *confgen.Gate, lim speed.Limits) measured {
	m := measured{res: proto.SpeedResult{ID: id}}
	d.post(func() { d.speedPhase(run, id, proto.SpeedPing) })
	var ms []int
	for i := range speedPings {
		delay, err := api.DelayURL(ctx, id, clash.PingURL, clash.ProbeTimeout)
		if ctx.Err() != nil {
			return m
		}
		if err != nil && clash.Unreachable(err) {
			m.gone = true
			return m
		}
		sample := proto.SpeedSample{ID: id, Phase: proto.SpeedPing}
		if err == nil {
			ms = append(ms, delay)
			m.pings = append(m.pings, stats.Answer{OK: true, Ms: delay, At: time.Now()})
			sample.Ping = &delay
		}
		d.srv.Publish(proto.TopicSpeed, sample)
		if err != nil && i == 0 {
			break // silent at once: no use waiting for it twice more
		}
	}
	m.at = time.Now()
	if len(ms) == 0 {
		m.res.Error = text("Сервер не ответил на проверку.")
		return m
	}
	mid, wobble := middle(ms)
	m.res.Ping, m.res.Jitter = &mid, &wobble

	d.post(func() { d.speedPhase(run, id, proto.SpeedDownload) })
	body, err := d.openSpeed(ctx, id, gate)
	if ctx.Err() != nil {
		return m
	}
	if err != nil {
		d.opts.Logf("speed test: %s: %v", id, err)
		m.res.Error = text("Сервер отвечает, но скачать через него не получилось.")
		return m
	}
	res, err := speed.Measure(ctx, body, lim, speedEvery, func(s speed.Sample) {
		d.srv.Publish(proto.TopicSpeed, proto.SpeedSample{
			ID: id, Phase: proto.SpeedDownload, Mbps: round1(s.Mbps), Bytes: s.Bytes, Elapsed: int(s.Elapsed.Milliseconds()),
		})
	})
	m.at = time.Now()
	m.res.Bytes = res.Bytes
	if ctx.Err() != nil {
		return m
	}
	if err != nil {
		d.opts.Logf("speed test: %s: after %d bytes: %v", id, res.Bytes, err)
	}
	if res.Bytes < speedMinBytes && (err != nil || res.Elapsed < lim.Time/2) {
		m.res.Error = text("Скачивание оборвалось в самом начале.")
		return m
	}
	m.mbps = res.Mbps
	mbps := round1(res.Mbps)
	m.res.Mbps = &mbps
	return m
}

// openSpeed starts the download: through the stand-in of a test, or through
// the gate to the first site that answers.
func (d *Daemon) openSpeed(ctx context.Context, id string, gate *confgen.Gate) (io.ReadCloser, error) {
	if d.opts.SpeedOpen != nil {
		return d.opts.SpeedOpen(ctx, id)
	}
	if gate == nil {
		return nil, errors.New("no gate")
	}
	addr := fmt.Sprintf("127.0.0.1:%d", gate.Port)
	var errs []error
	for _, u := range speed.URLs {
		// The start has a deadline, the download does not: Measure bounds
		// that. A request's context covers its body, so the deadline is a
		// timer that is stopped once the answer has come.
		octx, cancel := context.WithCancel(ctx)
		late := time.AfterFunc(speedOpenTimeout, cancel)
		body, err := speed.Open(octx, addr, confgen.GateUser(id), gate.Password, u)
		late.Stop()
		if err == nil {
			return cancelOnClose{body, cancel}, nil
		}
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// cancelOnClose keeps a request's context alive as long as its body is open
// and lets it go with the body.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// middle is the median of some pings and how far they are from it on
// average.
func middle(ms []int) (mid, wobble int) {
	s := append([]int(nil), ms...)
	sort.Ints(s)
	mid = s[len(s)/2]
	if len(s)%2 == 0 {
		mid = (s[len(s)/2-1] + s[len(s)/2]) / 2
	}
	sum := 0
	for _, x := range s {
		sum += max(x-mid, mid-x)
	}
	return mid, int(math.Round(float64(sum) / float64(len(s))))
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }

func text(s string) *string { return &s }
