// Package rules keeps the user's two lists: what goes through the VPN and
// what goes direct.
//
// The lists live in local rule-set files rather than in the engine's config:
// sing-box re-reads a local rule-set as soon as the file changes, so adding
// a program or a site needs no restart. The config only points at the files.
//
//	rules/direct.json      route: these go direct (checked first, so they win)
//	rules/vpn.json         route: these go through the "proxy" selector
//	rules/direct-dns.json  DNS: the domains of direct.json, resolved locally
//	rules/vpn-dns.json     DNS: the domains of vpn.json, resolved through the VPN
//	rules/servers-dns.json DNS: the VPN servers' own names, resolved locally
//
// The *-dns files carry domains only. An ip_cidr inside a rule-set that a DNS
// rule references turns that rule into a response filter, which would send
// every lookup through the VPN resolver first.
package rules

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"strelka/proto"
)

const (
	TagVPN        = "vpn"
	TagDirect     = "direct"
	TagServersDNS = "servers-dns"

	// ruleSetVersion is the source format version sing-box 1.11+ reads.
	ruleSetVersion = 3
)

//go:embed assets/vpn.json assets/direct.json
var defaults embed.FS

// List is one direction's rules, in the shape the protocol shows them.
type List = proto.RuleList

// Set is both lists.
type Set struct {
	VPN    List
	Direct List
}

// Invalid is a value that cannot be a rule. The text goes to the user.
type Invalid struct{ Text string }

func (e *Invalid) Error() string { return e.Text }

func invalid(format string, a ...any) error { return &Invalid{Text: fmt.Sprintf(format, a...)} }

func (s *Set) list(name string) *List {
	if name == TagVPN {
		return &s.VPN
	}
	return &s.Direct
}

func field(l *List, kind string) *[]string {
	switch kind {
	case proto.KindProcess:
		return &l.Processes
	case proto.KindDomain:
		return &l.Domains
	case proto.KindSuffix:
		return &l.Suffixes
	case proto.KindKeyword:
		return &l.Keywords
	case proto.KindIP:
		return &l.IPs
	}
	return nil
}

func allFields(l *List) []*[]string {
	return []*[]string{&l.Processes, &l.Domains, &l.Suffixes, &l.Keywords, &l.IPs}
}

// Count is the number of rules in both lists.
func (s *Set) Count() int {
	n := 0
	for _, l := range []*List{&s.VPN, &s.Direct} {
		for _, f := range allFields(l) {
			n += len(*f)
		}
	}
	return n
}

// File is the path of a rule-set file by its tag.
func File(dir, tag string) string { return filepath.Join(dir, tag+".json") }

// headless mirrors the rule-set source format for the fields in use.
type headless struct {
	ProcessName   []string `json:"process_name,omitempty"`
	Domain        []string `json:"domain,omitempty"`
	DomainSuffix  []string `json:"domain_suffix,omitempty"`
	DomainKeyword []string `json:"domain_keyword,omitempty"`
	IPCIDR        []string `json:"ip_cidr,omitempty"`
}

type ruleSetFile struct {
	Version int        `json:"version"`
	Rules   []headless `json:"rules"`
}

func parseList(raw []byte, name string) (List, error) {
	var l List
	var f ruleSetFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return l, fmt.Errorf("%s: %w", name, err)
	}
	for _, r := range f.Rules {
		l.Processes = append(l.Processes, r.ProcessName...)
		l.Domains = append(l.Domains, r.Domain...)
		l.Suffixes = append(l.Suffixes, r.DomainSuffix...)
		l.Keywords = append(l.Keywords, r.DomainKeyword...)
		l.IPs = append(l.IPs, r.IPCIDR...)
	}
	tidy(&l)
	return l, nil
}

func readList(path string) (List, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		var l List
		tidy(&l)
		return l, nil
	}
	if err != nil {
		return List{}, err
	}
	return parseList(raw, filepath.Base(path))
}

// Load reads both lists from dir. A missing file is an empty list.
func Load(dir string) (Set, error) {
	var s Set
	var err error
	if s.VPN, err = readList(File(dir, TagVPN)); err != nil {
		return s, err
	}
	s.Direct, err = readList(File(dir, TagDirect))
	return s, err
}

// Defaults are the lists a fresh install starts with.
func Defaults() Set {
	var s Set
	for tag, l := range map[string]*List{TagVPN: &s.VPN, TagDirect: &s.Direct} {
		raw, err := defaults.ReadFile("assets/" + tag + ".json")
		if err == nil {
			*l, err = parseList(raw, tag)
		}
		if err != nil {
			panic(fmt.Sprintf("rules: built-in %s list is broken: %v", tag, err))
		}
	}
	return s
}

// Ensure makes dir ready for the engine: every rule-set file the config
// names exists. On the first run the lists come from the built-in samples;
// later runs only put back files that went missing.
func Ensure(dir string) (Set, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Set{}, err
	}
	_, errV := os.Stat(File(dir, TagVPN))
	_, errD := os.Stat(File(dir, TagDirect))
	var s Set
	if errors.Is(errV, fs.ErrNotExist) && errors.Is(errD, fs.ErrNotExist) {
		s = Defaults()
	} else {
		var err error
		if s, err = Load(dir); err != nil {
			return s, err
		}
	}
	if err := Save(dir, s); err != nil {
		return s, err
	}
	if _, err := os.Stat(File(dir, TagServersDNS)); errors.Is(err, fs.ErrNotExist) {
		if err := WriteServersDNS(dir, nil); err != nil {
			return s, err
		}
	}
	return s, nil
}

// tidy sorts and dedupes every field. Processes keep their case:
// process_name is an exact match, so "Discord.exe" and "discord.exe" are
// different entries.
func tidy(l *List) {
	for _, p := range allFields(l) {
		v := slices.DeleteFunc(*p, func(s string) bool { return s == "" })
		slices.SortFunc(v, func(a, b string) int {
			if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
				return c
			}
			return strings.Compare(a, b)
		})
		*p = slices.Compact(v)
		if *p == nil {
			*p = []string{}
		}
	}
}

// Headless rules must not mix process_name with domains: inside one rule
// different kinds of condition are ANDed. Separate rules in a set are ORed.
func routeRules(l List) []headless {
	var out []headless
	if len(l.Processes) > 0 {
		out = append(out, headless{ProcessName: l.Processes})
	}
	if d := dnsRules(l); d != nil {
		out = append(out, d...)
	}
	if len(l.IPs) > 0 {
		out = append(out, headless{IPCIDR: l.IPs})
	}
	return out
}

func dnsRules(l List) []headless {
	if len(l.Domains)+len(l.Suffixes)+len(l.Keywords) == 0 {
		return nil
	}
	return []headless{{Domain: l.Domains, DomainSuffix: l.Suffixes, DomainKeyword: l.Keywords}}
}

func writeRuleSet(path string, rules []headless) error {
	if rules == nil {
		rules = []headless{}
	}
	b, err := json.MarshalIndent(ruleSetFile{Version: ruleSetVersion, Rules: rules}, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if cur, err := os.ReadFile(path); err == nil && string(cur) == string(b) {
		return nil // untouched file: no reload in sing-box
	}
	// Written in place, not via rename: sing-box watches the file itself.
	return os.WriteFile(path, b, 0o600)
}

// Save writes the four rule-set files. A file whose content did not change
// is not touched, so the engine reloads only what really changed.
func Save(dir string, s Set) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tidy(&s.VPN)
	tidy(&s.Direct)
	files := []struct {
		tag   string
		rules []headless
	}{
		{TagDirect, routeRules(s.Direct)},
		{TagDirect + "-dns", dnsRules(s.Direct)},
		{TagVPN, routeRules(s.VPN)},
		{TagVPN + "-dns", dnsRules(s.VPN)},
	}
	for _, f := range files {
		if err := writeRuleSet(File(dir, f.tag), f.rules); err != nil {
			return err
		}
	}
	return nil
}

// WriteServersDNS rewrites servers-dns.json: the names of the VPN servers,
// which must always resolve locally. In "all traffic" mode DNS goes through
// the VPN, and a lookup of the server's own name that had to travel through
// a tunnel to that very server would never complete.
func WriteServersDNS(dir string, hosts []string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var names []string
	for _, h := range hosts {
		h = strings.ToLower(h)
		if h != "" && net.ParseIP(h) == nil && !slices.Contains(names, h) {
			names = append(names, h)
		}
	}
	slices.Sort(names)
	var rules []headless
	if len(names) > 0 {
		rules = []headless{{Domain: names}}
	}
	return writeRuleSet(File(dir, TagServersDNS), rules)
}

// ---- editing -----------------------------------------------------------

// Normalize turns what the user typed or clicked into a rule. An empty kind
// means "work it out": a path or *.exe is a program, an address is an IP,
// anything else a site with all its subdomains.
func Normalize(kind, value string) (string, string, error) {
	return normalize(kind, value, runtime.GOOS == "windows")
}

func normalize(kind, value string, windows bool) (string, string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", "", invalid("Пустое значение.")
	}
	if kind == "" {
		lv := strings.ToLower(v)
		switch {
		case strings.HasSuffix(lv, ".exe") || strings.ContainsAny(v, `\`):
			kind = proto.KindProcess
		case !windows && strings.HasPrefix(v, "/"):
			kind = proto.KindProcess
		case isIPOrCIDR(v):
			kind = proto.KindIP
		default:
			kind = proto.KindSuffix
		}
	}

	switch kind {
	case proto.KindProcess:
		if i := strings.LastIndexAny(v, `\/`); i >= 0 {
			v = v[i+1:]
		}
		if windows && !strings.Contains(v, ".") {
			v += ".exe"
		}
		if v == "" || strings.ContainsAny(v, `:*?"<>|`) {
			return "", "", invalid("«%s» не похоже на имя программы.", value)
		}
	case proto.KindDomain, proto.KindSuffix:
		v = cleanDomain(v)
		if v == "" || strings.ContainsAny(v, " /\\:*") || !strings.Contains(v, ".") {
			return "", "", invalid("«%s» не похоже на адрес сайта.", value)
		}
	case proto.KindKeyword:
		v = strings.ToLower(v)
		if strings.ContainsAny(v, " /\\") {
			return "", "", invalid("Ключевое слово пишется без пробелов и слэшей.")
		}
	case proto.KindIP:
		p, err := parsePrefix(v)
		if err != nil {
			return "", "", invalid("«%s» не похоже на IP-адрес или подсеть.", v)
		}
		v = p.String()
	default:
		return "", "", invalid("Неизвестный вид правила: «%s».", kind)
	}
	return kind, v, nil
}

// cleanDomain accepts a pasted URL or "*.example.com" as well as a bare name.
func cleanDomain(v string) string {
	v = strings.ToLower(v)
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	}
	if i := strings.IndexAny(v, "/?#"); i >= 0 {
		v = v[:i]
	}
	if h, _, err := net.SplitHostPort(v); err == nil {
		v = h
	}
	v = strings.TrimPrefix(v, "*")
	v = strings.Trim(v, ".")
	// A suffix rule for the bare site covers www. and every other subdomain.
	if rest, ok := strings.CutPrefix(v, "www."); ok && strings.Contains(rest, ".") {
		v = rest
	}
	return v
}

func isIPOrCIDR(v string) bool {
	_, err := parsePrefix(v)
	return err == nil
}

func parsePrefix(v string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(v); err == nil {
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(v)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// ValidList reports whether name is one of the two lists.
func ValidList(name string) bool { return name == TagVPN || name == TagDirect }

// Add puts a value into one direction and takes it out of the other, so
// "move to VPN" and "add to VPN" are the same click. The value must already
// be normalized.
func (s *Set) Add(list, kind, value string) {
	other := TagDirect
	if list == TagDirect {
		other = TagVPN
	}
	s.Remove(other, kind, value)
	f := field(s.list(list), kind)
	*f = append(*f, value)
	tidy(s.list(list))
}

// Remove deletes a value and reports whether it was there.
func (s *Set) Remove(list, kind, value string) bool {
	f := field(s.list(list), kind)
	if f == nil {
		return false
	}
	n := len(*f)
	*f = slices.DeleteFunc(*f, func(x string) bool { return x == value })
	return len(*f) != n
}

// ---- answering "where will this go" --------------------------------------

// Match looks a normalized target up in one list and says which rule caught
// it: ("suffix", "example.com"). kind is that of the target, not of the rule:
// a site (suffix or domain) is tried against exact names, suffixes and
// keywords, like the engine does.
func Match(l List, kind, value string) (ruleKind, rule string, ok bool) {
	switch kind {
	case proto.KindProcess:
		if slices.Contains(l.Processes, value) {
			return proto.KindProcess, value, true
		}
	case proto.KindDomain, proto.KindSuffix:
		if slices.Contains(l.Domains, value) {
			return proto.KindDomain, value, true
		}
		for _, s := range l.Suffixes {
			if value == s || strings.HasSuffix(value, "."+s) {
				return proto.KindSuffix, s, true
			}
		}
		for _, k := range l.Keywords {
			if strings.Contains(value, k) {
				return proto.KindKeyword, k, true
			}
		}
	case proto.KindIP:
		target, err := netip.ParsePrefix(value)
		if err != nil {
			return "", "", false
		}
		for _, raw := range l.IPs {
			if p, err := netip.ParsePrefix(raw); err == nil && p.Bits() <= target.Bits() && p.Contains(target.Addr()) {
				return proto.KindIP, raw, true
			}
		}
	}
	return "", "", false
}
