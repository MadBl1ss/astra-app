package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"strelka/internal/clash"
	"strelka/internal/confgen"
	"strelka/internal/engine"
	"strelka/internal/profile"
	"strelka/internal/rules"
	"strelka/internal/store"
	"strelka/internal/xray"
	"strelka/proto"
)

// plan is everything the next engine start is built from: a frozen copy of
// what the profile and the settings say. The loop makes a new one whenever
// they change; the supervisors' goroutines only ever read the latest.
type plan struct {
	gen   uint64
	in    confgen.Input // without the Clash API address, which is per start
	xray  []xray.Server
	ports map[string]int // server id -> local port of its Xray inbound
}

// planFor builds a plan from a profile. It touches no loop state, so a
// background job can call it for a profile that is still a candidate. prev
// are the ports of the plan in use: a server keeps its port.
func (d *Daemon) planFor(p *profile.Profile, s proto.Settings, prev map[string]int) (*plan, error) {
	var tags []string
	for _, srv := range p.Servers {
		if srv.Via == proto.ViaXray {
			tags = append(tags, srv.ID)
		}
	}
	free := d.opts.PortFree
	if free == nil {
		free = xray.PortFree
	}
	ports, err := xray.AssignPorts(tags, prev, free)
	if err != nil {
		return nil, err
	}

	pl := &plan{ports: ports}
	pl.in = confgen.Input{
		Selected:       p.Selected,
		Mode:           p.Mode,
		IPv6:           s.IPv6,
		StrictRoute:    true, // a setting only if stage 04 finds it breaks something
		LocalProxyPort: s.LocalProxyPort,
		NoTUN:          d.opts.NoTUN,
	}
	for _, srv := range p.Servers {
		cs := confgen.Server{ID: srv.ID, Outbound: srv.Outbound}
		if srv.Via == proto.ViaXray {
			cs.XrayPort = ports[srv.ID]
			pl.xray = append(pl.xray, xray.Server{Tag: srv.ID, Port: cs.XrayPort, Outbound: srv.Xray})
		}
		pl.in.Servers = append(pl.in.Servers, cs)
	}
	if len(pl.xray) > 0 {
		pl.in.XrayMatch = confgen.ExactPath(d.opts.Paths.Engine(xrayName))
	}
	return pl, nil
}

// replan freezes the current profile and settings for the next start. It is
// cheap and is called after every change; whether the running engines need a
// restart is the caller's question.
func (d *Daemon) replan() {
	var prev map[string]int
	if cur := d.plan.Load(); cur != nil {
		prev = cur.ports
	}
	pl, err := d.planFor(d.prof, d.st.Settings, prev)
	if err != nil {
		d.opts.Logf("planning the next start: %v", err)
		return
	}
	d.planGen++
	pl.gen = d.planGen
	d.plan.Store(pl)
	d.xrayWanted = len(pl.xray) > 0

	hosts := make([]string, 0, len(d.prof.Servers))
	for _, s := range d.prof.Servers {
		hosts = append(hosts, s.Host)
	}
	if err := rules.WriteServersDNS(d.opts.Paths.RulesDir(), hosts); err != nil {
		d.opts.Logf("writing %s: %v", rules.TagServersDNS, err)
	}
}

// applyPlanChange is called when the set of servers or a setting that is
// baked into the config has changed: it replans and gets the engines onto
// the new plan.
func (d *Daemon) applyPlanChange(why string) {
	d.replan()
	if len(d.prof.Servers) == 0 {
		if d.st.VPN.Phase != proto.PhaseOff {
			d.log(proto.CatVPN, proto.LevelProblem, "Не осталось ни одного сервера — выключаю VPN.")
			d.vpnStop()
			return
		}
		d.setDesired(proto.DesiredOff)
		return
	}
	switch d.st.VPN.Phase {
	case proto.PhaseStarting, proto.PhaseOn, proto.PhaseRecovering:
		d.log(proto.CatVPN, proto.LevelInfo, "Перезапускаю VPN: "+why+".")
		d.restartEngines()
	case proto.PhaseFailed:
		// What failed may be exactly what has just been fixed.
		if d.st.VPN.Desired == proto.DesiredOn {
			d.beginStart()
		}
	}
}

func (d *Daemon) checkConfig(ctx context.Context, path string) error {
	if d.opts.CheckConfig != nil {
		return d.opts.CheckConfig(ctx, path)
	}
	exe := d.opts.Paths.Engine(boxName)
	if _, err := os.Stat(exe); err != nil {
		return err
	}
	return confgen.Check(ctx, exe, path, d.opts.Paths.Data)
}

func (d *Daemon) checkXray(ctx context.Context, path string) error {
	if d.opts.CheckXray != nil {
		return d.opts.CheckXray(ctx, path)
	}
	exe := d.opts.Paths.Engine(xrayName)
	if _, err := os.Stat(exe); err != nil {
		return err
	}
	return xray.Test(ctx, exe, path, d.opts.Paths.Data)
}

// hasXray reports whether the helper is installed. Without it the parser
// keeps to what sing-box can do alone.
func (d *Daemon) hasXray() bool {
	_, err := os.Stat(d.opts.Paths.Engine(xrayName))
	return err == nil
}

// vet builds both configs of a plan into scratch files and runs the engines'
// own checks on them. Nothing in use is touched.
func (d *Daemon) vet(ctx context.Context, pl *plan, suffix string) error {
	in := pl.in
	in.Controller, in.Secret = "127.0.0.1:9", "check"
	raw, err := confgen.Build(in)
	if err != nil {
		return err
	}
	tmp := d.opts.Paths.RunConfig() + suffix
	defer os.Remove(tmp)
	if err := store.WriteAtomic(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := d.checkConfig(ctx, tmp); err != nil {
		return err
	}
	if len(pl.xray) == 0 {
		return nil
	}
	xraw, err := xray.Build(pl.xray)
	if err != nil {
		return err
	}
	xtmp := d.opts.Paths.XrayConfig() + suffix
	defer os.Remove(xtmp)
	if err := store.WriteAtomic(xtmp, xraw, 0o600); err != nil {
		return err
	}
	return d.checkXray(ctx, xtmp)
}

func prepareFailed(err error) error {
	reason := engine.FailConfig
	if errors.Is(err, fs.ErrNotExist) {
		reason = engine.FailMissing
	}
	return &engine.PrepareError{Reason: reason, Err: err}
}

// prepareBox runs before every sing-box start, in the supervisor's
// goroutine. It reads the plan and immutable options, never the loop's state.
func (d *Daemon) prepareBox() (engine.Launch, func(context.Context) error, error) {
	pl := d.plan.Load()
	if pl == nil || len(pl.in.Servers) == 0 {
		return engine.Launch{}, nil, prepareFailed(confgen.ErrNoServers)
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
	in := pl.in
	in.Controller, in.Secret = fmt.Sprintf("127.0.0.1:%d", port), secret
	raw, err := confgen.Build(in)
	if err != nil {
		return engine.Launch{}, nil, prepareFailed(err)
	}

	cfgPath := d.opts.Paths.RunConfig()
	// The working config is replaced only by one the engine has accepted.
	// One check per plan is enough: a restart after a crash builds the same
	// config with another port.
	if d.checkedBox.Load() != pl.gen {
		tmp := cfgPath + ".new"
		if err := store.WriteAtomic(tmp, raw, 0o600); err != nil {
			return engine.Launch{}, nil, err
		}
		err := d.checkConfig(context.Background(), tmp)
		os.Remove(tmp)
		if err != nil {
			return engine.Launch{}, nil, prepareFailed(err)
		}
		d.checkedBox.Store(pl.gen)
	}
	if err := store.WriteAtomic(cfgPath, raw, 0o600); err != nil {
		return engine.Launch{}, nil, err
	}

	var l engine.Launch
	if d.opts.Launch != nil {
		l = d.opts.Launch(cfgPath)
	} else {
		l = engine.Launch{
			Exe: d.opts.Paths.Engine(boxName),
			// --disable-color: stderr is a pipe read by the daemon, not a terminal.
			Args: []string{"run", "-c", cfgPath, "-D", d.opts.Paths.Data, "--disable-color"},
			Dir:  d.opts.Paths.Data,
		}
	}
	api := clash.New(in.Controller, secret)
	d.api.Store(api)
	ready := func(ctx context.Context) error {
		_, err := api.Version(ctx)
		return err
	}
	return l, ready, nil
}

// prepareXray runs before every start of the helper.
func (d *Daemon) prepareXray() (engine.Launch, func(context.Context) error, error) {
	pl := d.plan.Load()
	if pl == nil || len(pl.xray) == 0 {
		return engine.Launch{}, nil, prepareFailed(errors.New("no server needs xray"))
	}
	raw, err := xray.Build(pl.xray)
	if err != nil {
		return engine.Launch{}, nil, prepareFailed(err)
	}
	cfgPath := d.opts.Paths.XrayConfig()
	if d.checkedXray.Load() != pl.gen {
		tmp := cfgPath + ".new"
		if err := store.WriteAtomic(tmp, raw, 0o600); err != nil {
			return engine.Launch{}, nil, err
		}
		err := d.checkXray(context.Background(), tmp)
		os.Remove(tmp)
		if err != nil {
			return engine.Launch{}, nil, prepareFailed(err)
		}
		d.checkedXray.Store(pl.gen)
	}
	if err := store.WriteAtomic(cfgPath, raw, 0o600); err != nil {
		return engine.Launch{}, nil, err
	}

	port := pl.xray[0].Port
	var l engine.Launch
	if d.opts.LaunchXray != nil {
		l = d.opts.LaunchXray(cfgPath, port)
	} else {
		l = engine.Launch{
			Exe:  d.opts.Paths.Engine(xrayName),
			Args: []string{"run", "-c", cfgPath},
			Dir:  d.opts.Paths.Data,
		}
	}
	// Xray has no API of ours to ask; listening on its first port is the
	// sign that it has read the config and is serving.
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	ready := func(ctx context.Context) error {
		dialer := net.Dialer{Timeout: time.Second}
		c, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return c.Close()
	}
	return l, ready, nil
}

// syncEngine makes a freshly started engine agree with the profile. The
// config already says the same, but the engine's cache.db remembers the mode
// and the server of its previous run and prefers them.
func (d *Daemon) syncEngine() {
	api := d.api.Load()
	if api == nil {
		return
	}
	mode, selected := confgen.ClashMode(d.prof.Mode), d.prof.Selected
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if cur, _, err := api.Mode(ctx); err != nil || !strings.EqualFold(cur, mode) {
			if err := api.SetMode(ctx, mode); err != nil {
				d.opts.Logf("setting the mode after start: %v", err)
			}
		}
		if selected != "" {
			if err := api.SwitchTo(ctx, confgen.SelectorTag, selected); err != nil {
				d.opts.Logf("selecting %s after start: %v", selected, err)
			}
		}
	}()
}
