// Package guard is the kill switch: while the VPN is meant to be on, only the
// engines, the daemon and the local network may reach the network past the
// tunnel.
//
// The package is split so that the part that can be wrong in interesting ways
// is tested without touching the system: Build returns the rules as plain
// data, and the platform file only translates them into filters.
package guard

import (
	"errors"
	"net/netip"
)

// ErrUnsupported is returned where the kill switch is not implemented yet.
var ErrUnsupported = errors.New("guard: not supported on this system")

// Guard installs and removes the filters. All methods are safe to repeat:
// the daemon calls them to bring the system to a wanted state, not to step
// through a sequence.
type Guard interface {
	// Engage installs the rules for spec, or moves an engaged guard to a new
	// spec without ever lifting the final block.
	Engage(spec Spec) error
	// SetTunnel permits the tunnel adapter with this LUID; 0 withdraws the
	// permit. It is remembered if the guard is not engaged yet.
	SetTunnel(luid uint64) error
	// Release removes everything.
	Release() error
	Status() Status
}

// Status is what guard.status reports.
type Status struct {
	Engaged bool   `json:"engaged"`
	Filters int    `json:"filters"`
	TunLUID uint64 `json:"tunLuid"`
}

// Spec is what the rules are built from.
type Spec struct {
	// Apps are full paths of the programs allowed out past the tunnel: the
	// engines and the daemon itself.
	Apps []string
	// AllowLAN keeps the router, printers and LAN games reachable.
	AllowLAN bool
}

type Action int

const (
	Permit Action = iota
	Block
)

type Dir int

const (
	Both Dir = iota
	Out      // connections this machine opens
	In       // connections it accepts
)

type Family int

const (
	AnyFamily Family = iota
	V4
	V6
)

type Proto int

const (
	UDP Proto = iota + 1
	TCP
	ICMPv6
)

// Weights. Within our sublayer the heavier rule wins and ends the search, so
// a permit and a block that can meet on one connection never share a weight.
const (
	// WeightOurs: loopback, the engines, the daemon, the tunnel.
	WeightOurs = 15
	// WeightDNS: the block that keeps name lookups from slipping out through
	// the "local network" permit - for most people the DNS server is the
	// router. Below WeightOurs so the engines still resolve and anything
	// inside the tunnel passes; above WeightKnown so the LAN permit loses.
	WeightDNS = 13
	// WeightKnown: what a network needs to stay configured, and the LAN.
	WeightKnown = 12
	// WeightRest: block everything else.
	WeightRest = 0
)

// Rule is one filter, described without the system's types. A zero field is
// not checked. Different fields must all match; several values of one field
// are alternatives - that is how WFP reads conditions, too.
type Rule struct {
	// Name is unique within a rule set and changes whenever the rule's
	// meaning does: a guard that is already engaged tells what to add and
	// what to drop by comparing names.
	Name   string
	Weight int
	Action Action
	Dir    Dir
	Family Family

	Loopback bool
	App      string // full path of the program
	Tunnel   bool   // the tunnel adapter; the platform fills in which one
	Protos   []Proto
	// LocalPort and RemotePort are UDP or TCP ports.
	LocalPort, RemotePort uint16
	// ICMPType is the ICMPv6 message type; meaningful with ICMPv6 only.
	ICMPType uint8
	Local    []netip.Prefix
	Remote   []netip.Prefix
}

// Layer is one of the four places a connection is authorised.
type Layer int

const (
	ConnectV4 Layer = iota
	ConnectV6
	AcceptV4
	AcceptV6
)

// Layers lists where the rule has to be installed: one filter per layer.
func (r Rule) Layers() []Layer {
	var out []Layer
	if r.Dir != In {
		if r.Family != V6 {
			out = append(out, ConnectV4)
		}
		if r.Family != V4 {
			out = append(out, ConnectV6)
		}
	}
	if r.Dir != Out {
		if r.Family != V6 {
			out = append(out, AcceptV4)
		}
		if r.Family != V4 {
			out = append(out, AcceptV6)
		}
	}
	return out
}

// TunnelRule permits everything on the tunnel adapter. It is kept apart from
// Build because it comes and goes with the adapter while the rest stays.
func TunnelRule() Rule {
	return Rule{Name: "tunnel", Weight: WeightOurs, Action: Permit, Tunnel: true}
}

func prefixes(list ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(list))
	for i, s := range list {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

var (
	lan4 = prefixes("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16",
		"224.0.0.0/4", "255.255.255.255/32")
	lan6 = prefixes("fe80::/10", "fc00::/7", "ff00::/8")

	linkLocal6   = prefixes("fe80::/10")
	dhcp6Servers = prefixes("ff02::1:2/128", "ff05::1:3/128")
	allRouters6  = prefixes("ff02::2/128")
)

// Build returns the rules for spec in the order they must be installed:
// heaviest first, the final block last. Installed in that order, a failure
// halfway leaves permits without a block - never a block without permits.
// The tunnel permit is not in the list, see TunnelRule.
func Build(spec Spec) []Rule {
	rules := []Rule{
		{Name: "loopback", Weight: WeightOurs, Action: Permit, Loopback: true},
	}
	seen := map[string]bool{}
	for _, app := range spec.Apps {
		if app == "" || seen[app] {
			continue
		}
		seen[app] = true
		rules = append(rules, Rule{Name: "app:" + app, Weight: WeightOurs, Action: Permit, App: app})
	}

	rules = append(rules,
		Rule{Name: "dns", Weight: WeightDNS, Action: Block, Dir: Out, Protos: []Proto{UDP, TCP}, RemotePort: 53},

		// Without DHCP the network does not fall at once but hours later,
		// when the lease runs out. The remote address is left open on the
		// way out: a renewal goes to the server directly, not to broadcast.
		Rule{Name: "dhcp4-out", Weight: WeightKnown, Action: Permit, Dir: Out, Family: V4,
			Protos: []Proto{UDP}, LocalPort: 68, RemotePort: 67},
		Rule{Name: "dhcp4-in", Weight: WeightKnown, Action: Permit, Dir: In, Family: V4,
			Protos: []Proto{UDP}, LocalPort: 68, RemotePort: 67},
		Rule{Name: "dhcp6-out", Weight: WeightKnown, Action: Permit, Dir: Out, Family: V6,
			Protos: []Proto{UDP}, LocalPort: 546, RemotePort: 547, Local: linkLocal6, Remote: dhcp6Servers},
		Rule{Name: "dhcp6-in", Weight: WeightKnown, Action: Permit, Dir: In, Family: V6,
			Protos: []Proto{UDP}, LocalPort: 546, RemotePort: 547, Local: linkLocal6, Remote: linkLocal6},

		// Neighbour discovery: without it IPv6 on the local link stops.
		// Router solicitation goes out, advertisement and redirect come in,
		// neighbour solicitation and advertisement go both ways.
		Rule{Name: "ndp-133", Weight: WeightKnown, Action: Permit, Dir: Out, Family: V6,
			Protos: []Proto{ICMPv6}, ICMPType: 133, Remote: allRouters6},
		Rule{Name: "ndp-134", Weight: WeightKnown, Action: Permit, Dir: In, Family: V6,
			Protos: []Proto{ICMPv6}, ICMPType: 134, Remote: linkLocal6},
		Rule{Name: "ndp-135", Weight: WeightKnown, Action: Permit, Family: V6,
			Protos: []Proto{ICMPv6}, ICMPType: 135},
		Rule{Name: "ndp-136", Weight: WeightKnown, Action: Permit, Family: V6,
			Protos: []Proto{ICMPv6}, ICMPType: 136},
		Rule{Name: "ndp-137", Weight: WeightKnown, Action: Permit, Dir: In, Family: V6,
			Protos: []Proto{ICMPv6}, ICMPType: 137, Remote: linkLocal6},
	)

	if spec.AllowLAN {
		rules = append(rules,
			Rule{Name: "lan4", Weight: WeightKnown, Action: Permit, Family: V4, Remote: lan4},
			Rule{Name: "lan6", Weight: WeightKnown, Action: Permit, Family: V6, Remote: lan6},
		)
	}

	return append(rules, Rule{Name: "rest", Weight: WeightRest, Action: Block})
}
