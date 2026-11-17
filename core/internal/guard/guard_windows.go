//go:build windows

package guard

import (
	"fmt"
	"net/netip"
	"sync"
	"unsafe"

	"github.com/tailscale/wf"
	"golang.org/x/sys/windows"
)

// The provider and the sublayer have fixed ids, so that Reset finds whatever
// any run of ours has left behind. Rules get random ones.
var (
	providerID = wf.ProviderID(windows.GUID{Data1: 0x5b3c1f6e, Data2: 0x8d24, Data3: 0x4c7a,
		Data4: [8]byte{0x9e, 0x15, 0x3a, 0x70, 0xc4, 0x1d, 0x62, 0xb8}})
	sublayerID = wf.SublayerID(windows.GUID{Data1: 0x5b3c1f6f, Data2: 0x8d24, Data3: 0x4c7a,
		Data4: [8]byte{0x9e, 0x15, 0x3a, 0x70, 0xc4, 0x1d, 0x62, 0xb8}})
)

const (
	providerName = "Strelka"
	sublayerName = "Strelka kill switch"
)

var layerIDs = map[Layer]wf.LayerID{
	ConnectV4: wf.LayerALEAuthConnectV4,
	ConnectV6: wf.LayerALEAuthConnectV6,
	AcceptV4:  wf.LayerALEAuthRecvAcceptV4,
	AcceptV6:  wf.LayerALEAuthRecvAcceptV6,
}

// wfp keeps the filters in a dynamic session: they live as long as the
// session handle does, so a daemon that dies takes them with it and cannot
// leave the machine cut off.
type wfp struct {
	logf func(format string, a ...any)

	mu        sync.Mutex
	sess      *wf.Session
	installed map[string][]wf.RuleID // by Rule.Name
	tun       uint64                 // the adapter the daemon wants permitted
	tunOn     uint64                 // the adapter the installed permit is for
}

// New returns the kill switch of this system. logf may be nil.
func New(logf func(format string, a ...any)) Guard {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &wfp{logf: logf, installed: map[string][]wf.RuleID{}}
}

func (g *wfp) Engage(spec Spec) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	fresh := g.sess == nil
	if fresh {
		if err := g.open(); err != nil {
			return err
		}
	}
	err := g.syncTunnel()
	if err == nil {
		err = g.apply(Build(spec))
	}
	if err != nil && fresh {
		// Half a guard is worse than none: it may block without the permits.
		g.close()
	}
	return err
}

func (g *wfp) SetTunnel(luid uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tun = luid
	if g.sess == nil {
		return nil
	}
	return g.syncTunnel()
}

func (g *wfp) Release() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.close()
}

func (g *wfp) Status() Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := Status{Engaged: g.sess != nil, TunLUID: g.tunOn}
	for _, ids := range g.installed {
		st.Filters += len(ids)
	}
	return st
}

func (g *wfp) open() error {
	sess, err := wf.New(&wf.Options{Name: sublayerName, Dynamic: true})
	if err != nil {
		return fmt.Errorf("guard: opening the filter engine: %w", err)
	}
	if err = sess.AddProvider(&wf.Provider{ID: providerID, Name: providerName}); err == nil {
		err = sess.AddSublayer(&wf.Sublayer{ID: sublayerID, Name: sublayerName, Provider: providerID})
	}
	if err != nil {
		sess.Close()
		return fmt.Errorf("guard: registering the provider: %w", err)
	}
	g.sess = sess
	return nil
}

// close drops the session and with it every filter, the provider and the
// sublayer at once.
func (g *wfp) close() error {
	if g.sess == nil {
		return nil
	}
	err := g.sess.Close()
	g.sess, g.tunOn = nil, 0
	g.installed = map[string][]wf.RuleID{}
	return err
}

// apply brings the installed rules to want: what is missing is added in the
// given order, then what is no longer wanted is removed. The final block is
// in every set, so it is never lifted on the way.
func (g *wfp) apply(want []Rule) error {
	keep := map[string]bool{TunnelRule().Name: true}
	for _, r := range want {
		keep[r.Name] = true
		if _, ok := g.installed[r.Name]; ok {
			continue
		}
		if err := g.install(r); err != nil {
			if r.App != "" && r.Action == Permit {
				// The id of a program is made from its file. An engine that
				// is not there cannot run either; go on without its permit.
				g.logf("guard: no permit for %s: %v", r.App, err)
				continue
			}
			return fmt.Errorf("guard: rule %q: %w", r.Name, err)
		}
	}
	for name := range g.installed {
		if !keep[name] {
			if err := g.remove(name); err != nil {
				return fmt.Errorf("guard: removing rule %q: %w", name, err)
			}
		}
	}
	return nil
}

// syncTunnel moves the tunnel permit to the adapter the daemon last named.
// The new permit goes in before the old one goes out.
func (g *wfp) syncTunnel() error {
	if g.tun == g.tunOn {
		return nil
	}
	name := TunnelRule().Name
	old := g.installed[name]
	delete(g.installed, name)
	g.tunOn = 0
	var err error
	if g.tun != 0 {
		if err = g.install(TunnelRule()); err == nil {
			g.tunOn = g.tun
		}
	}
	for _, id := range old {
		if e := g.sess.DeleteRule(id); e != nil && err == nil {
			err = e
		}
	}
	if err != nil {
		return fmt.Errorf("guard: tunnel permit: %w", err)
	}
	return nil
}

// install adds one rule: a filter on each of its layers, all or none.
func (g *wfp) install(r Rule) error {
	action := wf.ActionPermit
	if r.Action == Block {
		action = wf.ActionBlock
	}
	conds, err := g.conditions(r)
	if err != nil {
		return err
	}
	var ids []wf.RuleID
	for _, layer := range r.Layers() {
		guid, err := windows.GenerateGUID()
		if err == nil {
			err = g.sess.AddRule(&wf.Rule{
				ID:         wf.RuleID(guid),
				Name:       "Strelka: " + r.Name,
				Provider:   providerID,
				Sublayer:   sublayerID,
				Layer:      layerIDs[layer],
				Weight:     uint64(r.Weight),
				Conditions: conds,
				Action:     action,
			})
		}
		if err != nil {
			for _, id := range ids {
				g.sess.DeleteRule(id)
			}
			return err
		}
		ids = append(ids, wf.RuleID(guid))
	}
	g.installed[r.Name] = ids
	return nil
}

func (g *wfp) remove(name string) error {
	var first error
	for _, id := range g.installed[name] {
		if err := g.sess.DeleteRule(id); err != nil && first == nil {
			first = err
		}
	}
	delete(g.installed, name)
	return first
}

// conditions translates a rule. The value types are the ones the library
// maps for each field (compose.go: toValue0); a wrong one is an error from
// AddRule, not from the compiler.
func (g *wfp) conditions(r Rule) ([]*wf.Match, error) {
	var ms []*wf.Match
	eq := func(field wf.FieldID, value any) {
		ms = append(ms, &wf.Match{Field: field, Op: wf.MatchTypeEqual, Value: value})
	}
	if r.Loopback {
		ms = append(ms, &wf.Match{Field: wf.FieldFlags, Op: wf.MatchTypeFlagsAllSet, Value: wf.ConditionFlagIsLoopback})
	}
	if r.App != "" {
		id, err := wf.AppID(r.App)
		if err != nil {
			return nil, err
		}
		eq(wf.FieldALEAppID, id)
	}
	if r.Tunnel {
		eq(wf.FieldIPLocalInterface, g.tun)
	}
	for _, p := range r.Protos {
		switch p {
		case UDP:
			eq(wf.FieldIPProtocol, wf.IPProtoUDP)
		case TCP:
			eq(wf.FieldIPProtocol, wf.IPProtoTCP)
		case ICMPv6:
			eq(wf.FieldIPProtocol, wf.IPProtoICMPV6)
		}
	}
	if r.ICMPType != 0 {
		// At these layers the ICMP type and code stand in the port fields.
		eq(wf.FieldIPLocalPort, uint16(r.ICMPType))
		eq(wf.FieldIPRemotePort, uint16(0))
	} else {
		if r.LocalPort != 0 {
			eq(wf.FieldIPLocalPort, r.LocalPort)
		}
		if r.RemotePort != 0 {
			eq(wf.FieldIPRemotePort, r.RemotePort)
		}
	}
	addrs := func(field wf.FieldID, list []netip.Prefix) {
		for _, p := range list {
			if p.IsSingleIP() {
				eq(field, p.Addr())
			} else {
				eq(field, p)
			}
		}
	}
	addrs(wf.FieldIPLocalAddress, r.Local)
	addrs(wf.FieldIPRemoteAddress, r.Remote)
	return ms, nil
}

// Reset removes the provider with everything under it, whoever installed it
// and however: the uninstaller's and the user's way back to a working
// network. It reports how many filters it removed.
func Reset() (int, error) {
	sess, err := wf.New(&wf.Options{Name: sublayerName + " reset"})
	if err != nil {
		return 0, fmt.Errorf("guard: opening the filter engine: %w", err)
	}
	defer sess.Close()
	rules, err := sess.Rules()
	if err != nil {
		return 0, fmt.Errorf("guard: listing filters: %w", err)
	}
	n := 0
	var first error
	for _, r := range rules {
		if r.Provider != providerID {
			continue
		}
		if err := sess.DeleteRule(r.ID); err != nil {
			if first == nil {
				first = fmt.Errorf("guard: removing filter %q: %w", r.Name, err)
			}
			continue
		}
		n++
	}
	// Both are usually gone already: a dynamic session takes them with it.
	sess.DeleteSublayer(sublayerID)
	sess.DeleteProvider(providerID)
	return n, first
}

var procConvertInterfaceAliasToLuid = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("ConvertInterfaceAliasToLuid")

// TunnelLUID finds the adapter with this name. ok is false while there is
// no such adapter.
func TunnelLUID(alias string) (luid uint64, ok bool) {
	name, err := windows.UTF16PtrFromString(alias)
	if err != nil {
		return 0, false
	}
	status, _, _ := procConvertInterfaceAliasToLuid.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&luid)))
	return luid, status == 0 && luid != 0
}
