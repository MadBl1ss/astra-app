// Package subs downloads subscriptions and turns share links into servers.
// Everything here is a pure function of its input: no files, no state. Who
// owns the servers and where they are kept is the profile's business.
package subs

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"strelka/internal/xray"
	"strelka/proto"
)

// Server is one server of a subscription, ready for both config builders.
type Server struct {
	// ID is the stable tag: the outbound's tag in sing-box and in Xray, and
	// the server's id on the wire.
	ID       string `json:"id"`
	Name     string `json:"name"`
	Country  string `json:"country,omitempty"` // ISO code, "" when the name shows none
	Protocol string `json:"protocol"`          // vless | trojan | shadowsocks | hysteria2
	Via      string `json:"via"`               // sing-box | xray
	Host     string `json:"host"`
	Port     int    `json:"port"`

	// Outbound is the sing-box outbound, tag included. A server that goes
	// through Xray has none: sing-box reaches it over a local SOCKS port that
	// is only known when the config is built.
	Outbound json.RawMessage `json:"outbound,omitempty"`
	// Xray is the Xray outbound, tag included; nil when sing-box talks to the
	// server itself.
	Xray json.RawMessage `json:"xray,omitempty"`
}

// Skipped counts the links of one subscription that could not be used for
// one reason. The reason is a Russian text fit for the journal.
type Skipped struct {
	Reason string
	Count  int
}

// outbound covers every protocol the parser understands. Fields a protocol
// does not use stay empty and are dropped by omitempty.
type outbound struct {
	Type           string     `json:"type"`
	Tag            string     `json:"tag"`
	Server         string     `json:"server"`
	ServerPort     int        `json:"server_port"`
	UUID           string     `json:"uuid,omitempty"`
	Flow           string     `json:"flow,omitempty"`
	Method         string     `json:"method,omitempty"`
	Password       string     `json:"password,omitempty"`
	Plugin         string     `json:"plugin,omitempty"`
	PluginOpts     string     `json:"plugin_opts,omitempty"`
	Obfs           *obfsOpts  `json:"obfs,omitempty"`
	TLS            *tlsOpts   `json:"tls,omitempty"`
	Transport      *transport `json:"transport,omitempty"`
	PacketEncoding string     `json:"packet_encoding,omitempty"`
}

type tlsOpts struct {
	Enabled    bool         `json:"enabled"`
	ServerName string       `json:"server_name,omitempty"`
	Insecure   bool         `json:"insecure,omitempty"`
	ALPN       []string     `json:"alpn,omitempty"`
	UTLS       *utlsOpts    `json:"utls,omitempty"`
	Reality    *realityOpts `json:"reality,omitempty"`
}

type utlsOpts struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type realityOpts struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key,omitempty"`
	ShortID   string `json:"short_id,omitempty"`
}

type obfsOpts struct {
	Type     string `json:"type"`
	Password string `json:"password,omitempty"`
}

type transport struct {
	Type        string            `json:"type"`
	Path        string            `json:"path,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Host        string            `json:"host,omitempty"`
	ServiceName string            `json:"service_name,omitempty"`
}

var tagMarker = regexp.MustCompile(`\[([^\]]+)\]`)

// ReservedTags are the tags the config template uses itself; no server may
// take them.
func ReservedTags() map[string]int {
	return map[string]int{"proxy": 1, "direct": 1}
}

// ParseLinks turns share links into servers tagged "<prefix>-<label>". used
// holds the tags already taken and is extended with the new ones. With
// useXray, links that need Xray (see xray.Needs) become Xray servers; without
// it REALITY falls back to sing-box and XHTTP is skipped.
func ParseLinks(links []string, prefix string, used map[string]int, useXray bool) ([]Server, []Skipped) {
	type parsed struct {
		ob   *outbound
		xray map[string]any
		name string
		n    int // 1-based position in the subscription
	}
	var good []parsed
	skipped := map[string]int{}

	for i, link := range links {
		scheme, _, _ := strings.Cut(link, "://")
		scheme = strings.ToLower(scheme)
		var ob *outbound
		var xob map[string]any
		var name string
		var err error
		switch scheme {
		case "vless", "trojan":
			if u, host, port, e := xray.SplitLink(link); e == nil && useXray && xray.Needs(u.Query()) {
				xob, name, err = xray.Outbound(link)
				// sing-box gets a socks outbound later; this one only carries
				// the address through the checks below.
				typ := "vless"
				if scheme == "trojan" {
					typ = "trojan"
				}
				ob = &outbound{Type: typ, Server: host, ServerPort: port}
			} else if scheme == "vless" {
				ob, name, err = parseVLESS(link)
			} else {
				ob, name, err = parseTrojan(link)
			}
		case "ss":
			ob, name, err = parseSS(link)
		case "hysteria2", "hy2":
			ob, name, err = parseHysteria2(link)
		default:
			err = fmt.Errorf("протокол %s не поддерживается", scheme)
		}
		if err == nil && (ob.Server == "" || ob.Server == "0.0.0.0" || ob.ServerPort < 2) {
			// Panels put these in as notices ("App not supported", "limit
			// reached"), not as servers.
			err = errors.New("заглушка вместо сервера")
		}
		if err != nil {
			skipped[err.Error()]++
			continue
		}
		good = append(good, parsed{ob, xob, name, i + 1})
	}

	// Some panels shuffle the list on every request. Sorting by name keeps
	// "lider-DE2" the same server from one refresh to the next.
	sort.SliceStable(good, func(a, b int) bool { return good[a].name < good[b].name })

	var out []Server
	for _, p := range good {
		label, country := NameLabel(p.name)
		if label == "" {
			label = fmt.Sprintf("srv%d", p.n)
		}
		tag := UniqueTag(joinTag(prefix, label), used)
		s := Server{
			ID:       tag,
			Name:     strings.TrimSpace(p.name),
			Country:  country,
			Protocol: p.ob.Type,
			Via:      proto.ViaSingBox,
			Host:     p.ob.Server,
			Port:     p.ob.ServerPort,
		}
		if s.Name == "" {
			s.Name = tag
		}
		if p.xray != nil {
			p.xray["tag"] = tag
			s.Via = proto.ViaXray
			s.Xray, _ = json.Marshal(p.xray)
		} else {
			p.ob.Tag = tag
			s.Outbound, _ = json.Marshal(p.ob)
		}
		out = append(out, s)
	}

	var skips []Skipped
	for r, n := range skipped {
		skips = append(skips, Skipped{Reason: r, Count: n})
	}
	sort.Slice(skips, func(a, b int) bool { return skips[a].Reason < skips[b].Reason })
	return out, skips
}

func parseVLESS(link string) (*outbound, string, error) {
	u, host, port, err := xray.SplitLink(link)
	if err != nil {
		return nil, "", err
	}
	q := u.Query()
	if e := q.Get("encryption"); e != "" && e != "none" {
		return nil, "", errors.New("VLESS encryption не поддерживается sing-box")
	}
	ob := &outbound{
		Type:           "vless",
		Server:         host,
		ServerPort:     port,
		UUID:           u.User.Username(),
		Flow:           q.Get("flow"),
		PacketEncoding: "xudp",
	}
	ob.TLS = tlsFromQuery(q, q.Get("security"))
	if ob.Transport, err = transportFromQuery(q); err != nil {
		return nil, "", err
	}
	return ob, u.Fragment, nil
}

func parseTrojan(link string) (*outbound, string, error) {
	u, host, port, err := xray.SplitLink(link)
	if err != nil {
		return nil, "", err
	}
	q := u.Query()
	ob := &outbound{
		Type:       "trojan",
		Server:     host,
		ServerPort: port,
		Password:   u.User.Username(),
	}
	// Trojan runs over TLS unless the link explicitly says otherwise.
	ob.TLS = tlsFromQuery(q, orDefault(q.Get("security"), "tls"))
	if ob.Transport, err = transportFromQuery(q); err != nil {
		return nil, "", err
	}
	return ob, u.Fragment, nil
}

func parseHysteria2(link string) (*outbound, string, error) {
	u, host, port, err := xray.SplitLink(link)
	if err != nil {
		return nil, "", err
	}
	q := u.Query()
	pass := u.User.Username()
	if p, ok := u.User.Password(); ok {
		pass += ":" + p
	}
	ob := &outbound{
		Type:       "hysteria2",
		Server:     host,
		ServerPort: port,
		Password:   pass,
		TLS:        tlsFromQuery(q, "tls"),
	}
	if o := q.Get("obfs"); o != "" {
		ob.Obfs = &obfsOpts{Type: o, Password: q.Get("obfs-password")}
	}
	return ob, u.Fragment, nil
}

// parseSS reads both Shadowsocks link styles:
//
//	ss://BASE64(method:password)@host:port?plugin=...#name   (SIP002)
//	ss://BASE64(method:password@host:port)#name              (legacy)
//
// Not through url.Parse: base64 may carry '/' and '+', which it would take
// for a path.
func parseSS(link string) (*outbound, string, error) {
	_, body, _ := strings.Cut(link, "://")
	body, frag, _ := strings.Cut(body, "#")
	name, _ := url.PathUnescape(frag)
	body, query, _ := strings.Cut(body, "?")
	body = strings.TrimSuffix(body, "/")

	broken := errors.New("битая ссылка")
	userinfo, hostport, ok := cutLast(body, "@")
	if !ok {
		dec, err := decodeB64(body)
		if err != nil {
			return nil, "", broken
		}
		if userinfo, hostport, ok = cutLast(dec, "@"); !ok {
			return nil, "", broken
		}
	} else if un, err := url.PathUnescape(userinfo); err == nil && strings.Contains(un, ":") {
		userinfo = un // SIP022 keys go in plain, percent-encoded
	} else if dec, err := decodeB64(userinfo); err == nil {
		userinfo = dec
	} else {
		return nil, "", broken
	}

	method, pass, ok := strings.Cut(userinfo, ":")
	if !ok {
		return nil, "", broken
	}
	u, err := url.Parse("ss://" + hostport)
	if err != nil {
		return nil, "", broken
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return nil, "", errors.New("ссылка без порта")
	}

	ob := &outbound{
		Type:       "shadowsocks",
		Server:     u.Hostname(),
		ServerPort: port,
		Method:     method,
		Password:   pass,
	}
	if q, err := url.ParseQuery(query); err == nil {
		if p := q.Get("plugin"); p != "" {
			plugin, opts, _ := strings.Cut(p, ";")
			if plugin == "simple-obfs" {
				plugin = "obfs-local"
			}
			if plugin != "obfs-local" && plugin != "v2ray-plugin" {
				return nil, "", fmt.Errorf("плагин %s не поддерживается sing-box", plugin)
			}
			ob.Plugin, ob.PluginOpts = plugin, opts
		}
	}
	return ob, name, nil
}

func tlsFromQuery(q url.Values, sec string) *tlsOpts {
	if sec != "tls" && sec != "reality" {
		return nil
	}
	t := &tlsOpts{Enabled: true, ServerName: q.Get("sni")}
	if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" {
		t.Insecure = true
	}
	if a := q.Get("alpn"); a != "" {
		t.ALPN = strings.Split(a, ",")
	}
	fp := q.Get("fp")
	if sec == "reality" {
		// sing-box builds the Reality handshake with uTLS only.
		fp = orDefault(fp, "chrome")
		t.Reality = &realityOpts{Enabled: true, PublicKey: q.Get("pbk"), ShortID: q.Get("sid")}
	}
	if fp != "" {
		t.UTLS = &utlsOpts{Enabled: true, Fingerprint: fp}
	}
	return t
}

func transportFromQuery(q url.Values) (*transport, error) {
	switch typ := q.Get("type"); typ {
	case "", "tcp", "raw":
		if q.Get("headerType") == "http" {
			return nil, errors.New("TCP с HTTP-маскировкой не поддерживается sing-box")
		}
		return nil, nil
	case "ws":
		tr := &transport{Type: "ws", Path: orDefault(q.Get("path"), "/")}
		if h := q.Get("host"); h != "" {
			tr.Headers = map[string]string{"Host": h}
		}
		return tr, nil
	case "grpc":
		return &transport{Type: "grpc", ServiceName: q.Get("serviceName")}, nil
	case "httpupgrade":
		return &transport{Type: "httpupgrade", Path: orDefault(q.Get("path"), "/"), Host: q.Get("host")}, nil
	case "http", "h2":
		return &transport{Type: "http", Path: q.Get("path"), Host: q.Get("host")}, nil
	default:
		return nil, fmt.Errorf("транспорт %s не поддерживается sing-box", typ)
	}
}

// NameLabel turns a server's display name into a short tag and, when the
// name shows one, its exit country. Providers mark the server as "[FI]" or
// with a flag emoji, often both ("🇷🇺 [RU3] ..."); failing either, the first
// word of the name will do. The flag is the more reliable country: markers
// carry counters and nicknames like RU3 or SW.
func NameLabel(name string) (label, country string) {
	country = flagCountry(name)
	if m := tagMarker.FindStringSubmatch(name); m != nil {
		label = m[1]
	} else if country != "" {
		label = country
	} else {
		for _, w := range strings.FieldsFunc(name, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}) {
			if !isDigits(w) {
				label = w
				break
			}
		}
	}
	if r := []rune(label); len(r) > 20 {
		label = string(r[:20])
	}
	if country == "" && len(label) == 2 && strings.ToUpper(label) == label {
		country = label
	}
	return label, country
}

// flagCountry reads the first flag emoji: two regional indicator symbols,
// one per letter of the ISO country code.
func flagCountry(s string) string {
	r := []rune(s)
	for i := 0; i+1 < len(r); i++ {
		if isRegional(r[i]) && isRegional(r[i+1]) {
			return string([]rune{'A' + r[i] - 0x1F1E6, 'A' + r[i+1] - 0x1F1E6})
		}
	}
	return ""
}

func isRegional(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

func isDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func joinTag(prefix, label string) string {
	if prefix == "" {
		return label
	}
	return prefix + "-" + label
}

// UniqueTag returns tag, or tag with the first free number appended, and
// records the result in used.
func UniqueTag(tag string, used map[string]int) string {
	n, seen := used[tag]
	if !seen {
		used[tag] = 1
		return tag
	}
	for {
		n++
		t := fmt.Sprintf("%s%d", tag, n)
		if _, taken := used[t]; !taken {
			used[tag] = n
			used[t] = 1
			return t
		}
	}
}

func decodeB64(s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), nil
		}
	}
	return "", errors.New("не base64")
}

func cutLast(s, sep string) (before, after string, ok bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
