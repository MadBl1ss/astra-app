//go:build windows && wfp

package guard

// Real filters. Run by hand in an administrator's console, and only after
// the user said yes:
//
//	go test -tags wfp -run TestWFP -v ./internal/guard/
//
// The test installs the permits only - neither the final block nor the DNS
// block - so it checks the mechanics (do the conditions translate, are the
// filters listed, do they go away) without closing anything, another VPN's
// traffic included. Blocking is checked later, with
// `strelkad guard test --hold 20s`, which has a timer.

import (
	"os"
	"testing"

	"github.com/tailscale/wf"
)

// permits drops every rule that blocks.
func permits(all []Rule) []Rule {
	var out []Rule
	for _, r := range all {
		if r.Action == Permit {
			out = append(out, r)
		}
	}
	return out
}

func ours(t *testing.T) int {
	t.Helper()
	sess, err := wf.New(&wf.Options{Name: "Strelka guard test"})
	if err != nil {
		t.Fatalf("opening the filter engine (administrator?): %v", err)
	}
	defer sess.Close()
	rules, err := sess.Rules()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rules {
		if r.Provider == providerID {
			n++
		}
	}
	return n
}

func TestWFPInstallsListsAndRemoves(t *testing.T) {
	if n := ours(t); n != 0 {
		t.Fatalf("%d filters of ours are already installed; run `strelkad guard reset` first", n)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rules := permits(Build(Spec{Apps: []string{self}, AllowLAN: true}))
	want := 0
	for _, r := range rules {
		want += len(r.Layers())
	}

	g := New(t.Logf).(*wfp)
	if err := g.open(); err != nil {
		t.Fatal(err)
	}
	defer g.close()
	if err := g.apply(rules); err != nil {
		t.Fatal(err)
	}
	if got := g.Status().Filters; got != want {
		t.Errorf("guard counts %d filters, want %d", got, want)
	}
	if got := ours(t); got != want {
		t.Errorf("the system lists %d filters of ours, want %d", got, want)
	}

	// The tunnel permit: any existing adapter will do for the mechanics.
	if luid, ok := TunnelLUID("Strelka"); ok {
		g.tun = luid
		if err := g.syncTunnel(); err != nil {
			t.Errorf("tunnel permit: %v", err)
		}
	} else {
		t.Log("no adapter named Strelka: the tunnel permit was not exercised")
	}

	// Dropping the LAN must remove exactly its two rules.
	if err := g.apply(permits(Build(Spec{Apps: []string{self}}))); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.installed["lan4"]; ok {
		t.Error("lan4 is still installed after AllowLAN was turned off")
	}

	if err := g.close(); err != nil {
		t.Fatal(err)
	}
	if n := ours(t); n != 0 {
		t.Errorf("%d filters of ours are left after the session was closed", n)
	}
}
