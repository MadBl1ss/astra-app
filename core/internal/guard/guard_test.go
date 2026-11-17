package guard

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func find(rules []Rule, name string) (Rule, bool) {
	for _, r := range rules {
		if r.Name == name {
			return r, true
		}
	}
	return Rule{}, false
}

func must(t *testing.T, rules []Rule, name string) Rule {
	t.Helper()
	r, ok := find(rules, name)
	if !ok {
		t.Fatalf("no rule %q", name)
	}
	return r
}

var bothSpecs = []Spec{
	{Apps: []string{`C:\Strelka\sing-box.exe`, `C:\Strelka\xray.exe`, `C:\Strelka\strelkad.exe`}, AllowLAN: true},
	{Apps: []string{`C:\Strelka\sing-box.exe`}, AllowLAN: false},
	{},
}

func TestTheFinalBlockIsLastAndUnconditional(t *testing.T) {
	for _, spec := range bothSpecs {
		rules := Build(spec)
		last := rules[len(rules)-1]
		want := Rule{Name: "rest", Weight: WeightRest, Action: Block}
		if !reflect.DeepEqual(last, want) {
			t.Errorf("last rule = %+v, want the bare block", last)
		}
		if got := len(last.Layers()); got != 4 {
			t.Errorf("the final block sits on %d layers, want all 4", got)
		}
		for _, r := range rules[:len(rules)-1] {
			if r.Weight <= WeightRest {
				t.Errorf("rule %q weighs %d: it would not beat the final block", r.Name, r.Weight)
			}
		}
	}
}

// Installing in the returned order must never leave a block in place before
// the permits it would cut off.
func TestRulesComeHeaviestFirst(t *testing.T) {
	for _, spec := range bothSpecs {
		rules := Build(spec)
		for i := 1; i < len(rules); i++ {
			if rules[i].Weight > rules[i-1].Weight {
				t.Errorf("%q (%d) comes after %q (%d)", rules[i].Name, rules[i].Weight, rules[i-1].Name, rules[i-1].Weight)
			}
		}
	}
}

// The order of rules with one weight is not defined by the system.
func TestPermitsAndBlocksNeverShareAWeight(t *testing.T) {
	for _, spec := range bothSpecs {
		byWeight := map[int]Action{}
		for _, r := range append(Build(spec), TunnelRule()) {
			if prev, ok := byWeight[r.Weight]; ok && prev != r.Action {
				t.Errorf("weight %d holds both a permit and a block (%q)", r.Weight, r.Name)
			}
			byWeight[r.Weight] = r.Action
		}
	}
}

func TestNamesAreUnique(t *testing.T) {
	for _, spec := range bothSpecs {
		seen := map[string]bool{}
		for _, r := range append(Build(spec), TunnelRule()) {
			if seen[r.Name] {
				t.Errorf("two rules named %q", r.Name)
			}
			seen[r.Name] = true
		}
	}
}

func TestEveryAppIsPermittedOnceOnAllLayers(t *testing.T) {
	spec := Spec{Apps: []string{`C:\a\sing-box.exe`, "", `C:\a\xray.exe`, `C:\a\sing-box.exe`}}
	rules := Build(spec)
	var apps []string
	for _, r := range rules {
		if r.App == "" {
			continue
		}
		apps = append(apps, r.App)
		if r.Action != Permit || r.Weight != WeightOurs || len(r.Layers()) != 4 {
			t.Errorf("app rule %+v: want a permit of the top weight on all layers", r)
		}
	}
	if want := []string{`C:\a\sing-box.exe`, `C:\a\xray.exe`}; !reflect.DeepEqual(apps, want) {
		t.Errorf("apps = %q, want %q", apps, want)
	}
}

// The reason the DNS block exists: with the LAN allowed, a lookup sent to the
// router must still lose, while the engines and the tunnel still win.
func TestDNSBlockSitsBetweenOursAndTheLAN(t *testing.T) {
	rules := Build(Spec{Apps: []string{`C:\a\sing-box.exe`}, AllowLAN: true})
	dns := must(t, rules, "dns")
	if dns.Action != Block || dns.RemotePort != 53 || dns.Dir != Out {
		t.Fatalf("dns rule = %+v", dns)
	}
	if !reflect.DeepEqual(dns.Protos, []Proto{UDP, TCP}) {
		t.Errorf("dns protocols = %v, want UDP and TCP", dns.Protos)
	}
	for _, name := range []string{"lan4", "lan6"} {
		if lan := must(t, rules, name); lan.Weight >= dns.Weight {
			t.Errorf("%s (%d) is not below the DNS block (%d)", name, lan.Weight, dns.Weight)
		}
	}
	for _, r := range append(rules, TunnelRule()) {
		if (r.App != "" || r.Tunnel || r.Loopback) && r.Weight <= dns.Weight {
			t.Errorf("%q (%d) is not above the DNS block (%d)", r.Name, r.Weight, dns.Weight)
		}
	}
}

func TestLANIsPermittedOnlyWhenAllowed(t *testing.T) {
	off := Build(Spec{})
	for _, r := range off {
		if strings.HasPrefix(r.Name, "lan") {
			t.Errorf("rule %q without AllowLAN", r.Name)
		}
	}
	on := Build(Spec{AllowLAN: true})
	lan4, lan6 := must(t, on, "lan4"), must(t, on, "lan6")
	for _, addr := range []string{"192.168.1.1", "10.20.30.40", "172.31.255.1", "169.254.7.7", "224.0.0.251", "255.255.255.255"} {
		if !contains(lan4.Remote, addr) {
			t.Errorf("lan4 does not cover %s", addr)
		}
	}
	for _, addr := range []string{"8.8.8.8", "172.32.0.1", "100.64.0.1"} {
		if contains(lan4.Remote, addr) {
			t.Errorf("lan4 covers %s, which is not local", addr)
		}
	}
	for _, addr := range []string{"fe80::1", "fd00::1", "ff02::fb"} {
		if !contains(lan6.Remote, addr) {
			t.Errorf("lan6 does not cover %s", addr)
		}
	}
	if contains(lan6.Remote, "2a00:1450::1") {
		t.Error("lan6 covers a global address")
	}
	// The rest of the set does not depend on the setting.
	if len(on) != len(off)+2 {
		t.Errorf("AllowLAN changed %d rules, want exactly the two LAN permits", len(on)-len(off))
	}
}

func contains(list []netip.Prefix, addr string) bool {
	a := netip.MustParseAddr(addr)
	for _, p := range list {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// An IPv4 address in a filter on an IPv6 layer is refused by the system, and
// a rule for both families cannot name addresses at all.
func TestAddressesMatchTheRuleFamily(t *testing.T) {
	for _, r := range Build(Spec{AllowLAN: true}) {
		for _, p := range append(append([]netip.Prefix{}, r.Local...), r.Remote...) {
			switch {
			case r.Family == AnyFamily:
				t.Errorf("%q is for both families but names %s", r.Name, p)
			case p.Addr().Is4() != (r.Family == V4):
				t.Errorf("%q: %s is of the wrong family", r.Name, p)
			}
		}
		if r.ICMPType != 0 && (r.Family != V6 || !reflect.DeepEqual(r.Protos, []Proto{ICMPv6})) {
			t.Errorf("%q has an ICMPv6 type but is not an ICMPv6 rule for IPv6", r.Name)
		}
		if (r.LocalPort != 0 || r.RemotePort != 0) && len(r.Protos) == 0 {
			t.Errorf("%q names a port without a protocol", r.Name)
		}
	}
}

func TestWhatKeepsTheNetworkConfiguredIsPermitted(t *testing.T) {
	rules := Build(Spec{})
	for _, name := range []string{"dhcp4-out", "dhcp4-in", "dhcp6-out", "dhcp6-in", "ndp-133", "ndp-134", "ndp-135", "ndp-136", "ndp-137"} {
		if r := must(t, rules, name); r.Action != Permit || r.Weight != WeightKnown {
			t.Errorf("%s = %+v, want a permit of weight %d", name, r, WeightKnown)
		}
	}
}

func TestLayers(t *testing.T) {
	cases := []struct {
		rule Rule
		want []Layer
	}{
		{Rule{}, []Layer{ConnectV4, ConnectV6, AcceptV4, AcceptV6}},
		{Rule{Dir: Out}, []Layer{ConnectV4, ConnectV6}},
		{Rule{Dir: In, Family: V6}, []Layer{AcceptV6}},
		{Rule{Family: V4}, []Layer{ConnectV4, AcceptV4}},
		{Rule{Dir: Out, Family: V4}, []Layer{ConnectV4}},
	}
	for _, c := range cases {
		if got := c.rule.Layers(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Layers(dir %d, family %d) = %v, want %v", c.rule.Dir, c.rule.Family, got, c.want)
		}
	}
}

func TestTunnelRuleBeatsEverythingButItsPeers(t *testing.T) {
	tun := TunnelRule()
	if tun.Action != Permit || !tun.Tunnel || tun.Weight != WeightOurs || len(tun.Layers()) != 4 {
		t.Errorf("tunnel rule = %+v", tun)
	}
}
