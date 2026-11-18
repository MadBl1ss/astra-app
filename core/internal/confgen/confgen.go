// Package confgen builds the engine's config. The old tray edited
// config.json in place; here it is assembled from scratch every time, from
// the built-in template, the profile and the settings, and nobody edits the
// result by hand.
//
// What the template changes against the old package/config.json:
//   - no "auto" group, and no "direct" inside the "proxy" selector: with the
//     kill switch in mind, "VPN on" must never quietly mean "direct";
//   - the selector interrupts open connections when the server changes;
//   - the TUN is named "Strelka", has an IPv6 address and strict_route;
//   - the log goes to stderr, where the daemon reads it;
//   - no web panel;
//   - the Xray helper is exempted by its full path, not by the name
//     "xray.exe" that any program could take;
//   - programs of the direct list go before the sniffer (direct-early).
//     The sniffer holds a new TCP connection for up to 300 ms waiting for
//     its first bytes, and a protocol where the server speaks first (game
//     boosters, mail, databases) waits all of it, every time. A program that
//     goes direct whatever site it talks to needs no sniffing. Its DNS still
//     goes after the sniffer, to be answered by the engine.
package confgen

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"strelka/internal/runx"
	"strelka/internal/xray"
	"strelka/proto"
)

//go:embed assets/template.json assets/rules/*.srs
var assets embed.FS

// The Clash API's names for the two modes; the template's rules carry them
// as clash_mode.
const (
	ClashRule   = "Rule"
	ClashGlobal = "Global"

	// SelectorTag is the one group every routed connection goes through.
	SelectorTag = "proxy"
	// DirectTag is the outbound that leaves past the VPN.
	DirectTag = "direct"
	// TUNName is the adapter the engine creates. The network monitor must
	// not take its routes for a way out.
	TUNName = "Strelka"
	// DevProxyPort is the local proxy of a daemon run without TUN.
	DevProxyPort = 20808
)

// ClashMode maps the protocol's mode to the Clash API's.
func ClashMode(mode string) string {
	if mode == proto.ModeGlobal {
		return ClashGlobal
	}
	return ClashRule
}

// ErrNoServers is returned for a profile without servers. There is no
// config for it: a selector needs at least one member, and the only member
// that needs no server is "direct".
var ErrNoServers = errors.New("no servers")

// Server is one server as the config needs it.
type Server struct {
	ID string
	// Outbound is the sing-box outbound; unused when XrayPort is set.
	Outbound json.RawMessage
	// XrayPort is the local SOCKS port of a server that goes through Xray.
	XrayPort int
}

// Input is everything a config depends on.
type Input struct {
	Servers  []Server
	Selected string // falls back to the first server
	Mode     string // proto.ModeRule | proto.ModeGlobal

	IPv6        string // proto.IPv6Tunnel: IPv6 goes into the TUN; proto.IPv6Block: IPv4 only
	StrictRoute bool
	// LocalProxyPort adds a mixed (HTTP + SOCKS) proxy on 127.0.0.1; 0 is off.
	LocalProxyPort int
	// NoTUN leaves the TUN out: the development mode that needs no admin
	// rights and does not fight a VPN already running on the machine.
	NoTUN bool

	// XrayMatch is the regular expression for the Xray helper's full path
	// (see ExactPath); empty drops the exemption rule.
	XrayMatch string

	// SelfMatch (the daemon's own full path, see ExactPath) and SelfHosts
	// send the daemon's requests to those hosts past the VPN, whatever the
	// mode: it asks the geo service where the computer is, and through the
	// VPN the answer would be the server's country. Both conditions hold at
	// once, so a browser on the same site goes the usual way. Either one
	// empty drops the rule.
	SelfMatch string
	SelfHosts []string

	Controller string // Clash API address, host:port
	Secret     string
}

// ExactPath is a process_path_regex that matches exactly one file. On
// Windows the comparison ignores case: the system reports a process's path
// with the casing on disk, which need not be the one the daemon was given.
func ExactPath(path string) string {
	if path == "" {
		return ""
	}
	re := "^" + regexp.QuoteMeta(path) + "$"
	if runtime.GOOS == "windows" {
		re = "(?i)" + re
	}
	return re
}

// Build assembles config.json.
func Build(in Input) ([]byte, error) {
	if len(in.Servers) == 0 {
		return nil, ErrNoServers
	}
	raw, err := assets.ReadFile("assets/template.json")
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	// Keep numbers as written; float64 would turn 9000 into 9e+03.
	dec.UseNumber()
	var cfg map[string]any
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}

	localProxy := in.LocalProxyPort
	if in.NoTUN && localProxy == 0 {
		localProxy = DevProxyPort
	}

	// Inbounds.
	var inbounds []any
	for _, item := range cfg["inbounds"].([]any) {
		ib := item.(map[string]any)
		switch marker(ib) {
		case "tun":
			if in.NoTUN {
				continue
			}
			if in.IPv6 == proto.IPv6Block {
				// With strict_route an address family the TUN does not carry
				// is unreachable, which is the point.
				ib["address"] = firstOnly(ib["address"])
			}
			ib["strict_route"] = in.StrictRoute
		case "local-proxy":
			if localProxy == 0 {
				continue
			}
			ib["listen_port"] = localProxy
		}
		inbounds = append(inbounds, ib)
	}
	cfg["inbounds"] = inbounds

	// Outbounds: the selector, the servers, direct.
	selected := in.Servers[0].ID
	tags := make([]any, 0, len(in.Servers))
	servers := make([]any, 0, len(in.Servers))
	for _, s := range in.Servers {
		if s.ID == in.Selected {
			selected = s.ID
		}
		tags = append(tags, s.ID)
		if s.XrayPort > 0 {
			servers = append(servers, xray.SocksTo(s.ID, s.XrayPort))
			continue
		}
		if len(s.Outbound) == 0 {
			return nil, fmt.Errorf("server %s has no outbound", s.ID)
		}
		servers = append(servers, s.Outbound)
	}
	var outbounds []any
	for _, item := range cfg["outbounds"].([]any) {
		ob := item.(map[string]any)
		if ob["tag"] == SelectorTag {
			ob["outbounds"] = tags
			ob["default"] = selected
			outbounds = append(outbounds, ob)
			outbounds = append(outbounds, servers...)
			continue
		}
		outbounds = append(outbounds, ob)
	}
	cfg["outbounds"] = outbounds

	// Route rules that depend on the input.
	route := cfg["route"].(map[string]any)
	var rules []any
	for _, item := range route["rules"].([]any) {
		r := item.(map[string]any)
		switch marker(r) {
		case "xray":
			if in.XrayMatch == "" {
				continue
			}
			r["process_path_regex"] = []any{in.XrayMatch}
		case "self":
			if in.SelfMatch == "" || len(in.SelfHosts) == 0 {
				continue
			}
			r["process_path_regex"] = []any{in.SelfMatch}
			hosts := make([]any, len(in.SelfHosts))
			for i, h := range in.SelfHosts {
				hosts[i] = h
			}
			r["domain"] = hosts
		case "local-proxy":
			if localProxy == 0 {
				continue
			}
		case "direct-early":
			if in.NoTUN {
				continue // it is about the TUN's connections and names its inbound
			}
		}
		rules = append(rules, r)
	}
	route["rules"] = rules

	api := cfg["experimental"].(map[string]any)["clash_api"].(map[string]any)
	api["external_controller"] = in.Controller
	api["secret"] = in.Secret
	api["default_mode"] = ClashMode(in.Mode)

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// marker reads and removes the template's "_strelka" note from an item.
func marker(item map[string]any) string {
	m, _ := item["_strelka"].(string)
	delete(item, "_strelka")
	return m
}

func firstOnly(v any) any {
	if list, ok := v.([]any); ok && len(list) > 1 {
		return list[:1]
	}
	return v
}

// AutoLists are the tags of the lists the engine downloads and refreshes
// itself.
func AutoLists() []string { return []string{"itdog-inside", "itdog-google-ai"} }

// AutoListFile is where the initial copy of an auto list lies.
func AutoListFile(rulesDir, tag string) string { return filepath.Join(rulesDir, tag+".srs") }

// WriteAutoLists puts the built-in copies of the auto lists into rulesDir
// unless they are already there. sing-box loads them before its first
// download, so the VPN starts even when GitHub is unreachable.
func WriteAutoLists(rulesDir string) error {
	if err := os.MkdirAll(rulesDir, 0o700); err != nil {
		return err
	}
	for _, tag := range AutoLists() {
		path := AutoListFile(rulesDir, tag)
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		raw, err := assets.ReadFile("assets/rules/" + tag + ".srs")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// Check runs "sing-box check" on a config. dataDir is the engine's working
// folder: the config names its rule-set files relative to it.
func Check(ctx context.Context, exe, configPath, dataDir string) error {
	out, err := run(ctx, 30*time.Second, exe, dataDir, "check", "-c", configPath, "-D", dataDir, "--disable-color")
	if err != nil {
		return fmt.Errorf("sing-box check: %w: %s", err, lastLine(out))
	}
	return nil
}

// MatchAutoList asks the engine whether a domain or an IP is in a compiled
// rule-set file.
func MatchAutoList(ctx context.Context, exe, srsPath, target string) (bool, error) {
	out, err := run(ctx, 10*time.Second, exe, filepath.Dir(srsPath), "rule-set", "match", "-f", "binary", srsPath, target, "--disable-color")
	if err != nil {
		return false, fmt.Errorf("sing-box rule-set match: %w: %s", err, lastLine(out))
	}
	// A hit is reported as "match rules.[0]: ..."; a miss prints nothing.
	return bytes.Contains(out, []byte("match rules")), nil
}

// CountAutoList counts the entries of a compiled rule-set file.
func CountAutoList(ctx context.Context, exe, srsPath string) (int, error) {
	tmp, err := os.CreateTemp("", "strelka-list-*.json")
	if err != nil {
		return 0, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if out, err := run(ctx, 20*time.Second, exe, filepath.Dir(srsPath), "rule-set", "decompile", srsPath, "-o", tmp.Name(), "--disable-color"); err != nil {
		return 0, fmt.Errorf("sing-box rule-set decompile: %w: %s", err, lastLine(out))
	}
	raw, err := os.ReadFile(tmp.Name())
	if err != nil {
		return 0, err
	}
	var set struct {
		Rules []map[string]json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		return 0, err
	}
	n := 0
	for _, rule := range set.Rules {
		for _, v := range rule {
			var list []json.RawMessage
			if json.Unmarshal(v, &list) == nil {
				n += len(list)
			} else {
				n++
			}
		}
	}
	return n, nil
}

func run(ctx context.Context, limit time.Duration, exe, dir string, args ...string) ([]byte, error) {
	return runx.Output(ctx, limit, exe, dir, args...)
}

func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
