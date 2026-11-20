// Package xray builds the config of the Xray helper.
//
// Some servers only work through Xray-core, so xray runs next to sing-box.
// sing-box keeps the TUN and the routing; each such server becomes a local
// SOCKS port in xray.json, and in config.json a socks outbound with the
// server's tag points at that port.
//
// Two kinds go to Xray:
//   - REALITY: sing-box introduces itself to the server as Xray 1.8.1, and
//     panels that set a minimum client version reject it ("reality
//     verification failed");
//   - XHTTP, which sing-box does not implement at all.
//
// Without the xray binary the parser falls back to what sing-box can do alone.
package xray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"strelka/internal/runx"
)

// BasePort is where the local SOCKS ports start. The old tray used 17100 and
// up; a different range lets the two run side by side during development and
// while a user moves over, without one taking the ports the other's Xray is
// about to bind.
const BasePort = 17400

// ProbeBasePort is where the ports of the server test's own Xray start. It
// may run while the VPN's helper starts up, so the two never share a port.
const ProbeBasePort = 27400

// Needs tells whether a vless/trojan link must go through Xray.
func Needs(q url.Values) bool {
	switch q.Get("type") {
	case "xhttp", "splithttp":
		return true
	}
	if e := q.Get("encryption"); e != "" && e != "none" {
		return true // VLESS post-quantum encryption: Xray only
	}
	return q.Get("security") == "reality"
}

// SplitLink parses a URL-shaped share link and checks it names a server.
func SplitLink(link string) (*url.URL, string, int, error) {
	u, err := url.Parse(link)
	if err != nil || u.User == nil {
		return nil, "", 0, errors.New("битая ссылка")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return nil, "", 0, errors.New("ссылка без порта")
	}
	return u, u.Hostname(), port, nil
}

// Outbound builds the Xray outbound for a vless:// or trojan:// link, without
// a tag, and returns the server's display name. Xray reads these links'
// parameters natively, so this is mostly renaming.
func Outbound(link string) (map[string]any, string, error) {
	u, host, port, err := SplitLink(link)
	if err != nil {
		return nil, "", err
	}
	q := u.Query()
	scheme := strings.ToLower(u.Scheme)

	ob := map[string]any{"protocol": scheme}
	switch scheme {
	case "vless":
		user := map[string]any{"id": u.User.Username(), "encryption": orDefault(q.Get("encryption"), "none")}
		if f := q.Get("flow"); f != "" {
			user["flow"] = f
		}
		ob["settings"] = map[string]any{"vnext": []any{map[string]any{
			"address": host, "port": port, "users": []any{user},
		}}}
	case "trojan":
		ob["settings"] = map[string]any{"servers": []any{map[string]any{
			"address": host, "port": port, "password": u.User.Username(),
		}}}
	default:
		return nil, "", fmt.Errorf("протокол %s не поддерживается", scheme)
	}

	ss, err := stream(q, scheme)
	if err != nil {
		return nil, "", err
	}
	ob["streamSettings"] = ss
	return ob, u.Fragment, nil
}

func stream(q url.Values, scheme string) (map[string]any, error) {
	ss := map[string]any{}

	network := q.Get("type")
	switch network {
	case "", "tcp", "raw":
		network = "raw"
		if q.Get("headerType") == "http" {
			req := map[string]any{"path": []string{orDefault(q.Get("path"), "/")}}
			if h := q.Get("host"); h != "" {
				req["headers"] = map[string]any{"Host": strings.Split(h, ",")}
			}
			ss["rawSettings"] = map[string]any{"header": map[string]any{"type": "http", "request": req}}
		}
	case "ws":
		ss["wsSettings"] = map[string]any{"path": orDefault(q.Get("path"), "/"), "host": q.Get("host")}
	case "httpupgrade":
		ss["httpupgradeSettings"] = map[string]any{"path": orDefault(q.Get("path"), "/"), "host": q.Get("host")}
	case "grpc":
		ss["grpcSettings"] = map[string]any{"serviceName": q.Get("serviceName"), "multiMode": q.Get("mode") == "multi"}
	case "xhttp", "splithttp":
		network = "xhttp"
		xs := map[string]any{"path": orDefault(q.Get("path"), "/"), "host": q.Get("host"), "mode": orDefault(q.Get("mode"), "auto")}
		if extra := q.Get("extra"); extra != "" {
			if !json.Valid([]byte(extra)) {
				return nil, errors.New("битый параметр extra у XHTTP")
			}
			xs["extra"] = json.RawMessage(extra)
		}
		ss["xhttpSettings"] = xs
	default:
		return nil, fmt.Errorf("транспорт %s не поддерживается", network)
	}
	ss["network"] = network

	sec := q.Get("security")
	if scheme == "trojan" && sec == "" {
		sec = "tls"
	}
	switch sec {
	case "reality":
		rs := map[string]any{
			"serverName":  q.Get("sni"),
			"fingerprint": orDefault(q.Get("fp"), "chrome"),
			"publicKey":   q.Get("pbk"),
			"shortId":     q.Get("sid"),
		}
		if v := q.Get("spx"); v != "" {
			rs["spiderX"] = v
		}
		if v := q.Get("pqv"); v != "" {
			rs["mldsa65Verify"] = v
		}
		ss["security"] = "reality"
		ss["realitySettings"] = rs
	case "tls":
		ts := map[string]any{"serverName": q.Get("sni")}
		if v := q.Get("fp"); v != "" {
			ts["fingerprint"] = v
		}
		if v := q.Get("alpn"); v != "" {
			ts["alpn"] = strings.Split(v, ",")
		}
		if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" {
			ts["allowInsecure"] = true
		}
		ss["security"] = "tls"
		ss["tlsSettings"] = ts
	case "", "none":
		ss["security"] = "none"
	default:
		return nil, fmt.Errorf("защита %s не поддерживается", sec)
	}

	// "fm" carries Xray's finalmask as is, e.g. TLS ClientHello fragmenting
	// for servers that DPI otherwise cuts off.
	if fm := q.Get("fm"); fm != "" && json.Valid([]byte(fm)) {
		ss["finalmask"] = json.RawMessage(fm)
	}
	return ss, nil
}

// Server is one server handled by Xray: its outbound (tag included) and the
// local port sing-box reaches it on.
type Server struct {
	Tag      string
	Port     int
	Outbound json.RawMessage
}

// Build lays out xray.json: one SOCKS inbound per server on 127.0.0.1, each
// routed to its own outbound. The log goes to stderr, where the daemon reads
// it like sing-box's.
func Build(servers []Server) ([]byte, error) {
	inbounds, outbounds, rules := []any{}, []any{}, []any{}
	for _, s := range servers {
		in := "in-" + s.Tag
		inbounds = append(inbounds, map[string]any{
			"tag": in, "listen": "127.0.0.1", "port": s.Port, "protocol": "socks",
			"settings": map[string]any{"udp": true, "ip": "127.0.0.1"},
		})
		outbounds = append(outbounds, s.Outbound)
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{in}, "outboundTag": s.Tag})
	}
	outbounds = append(outbounds, map[string]any{"tag": "direct", "protocol": "freedom"})
	cfg := map[string]any{
		"log":       map[string]any{"loglevel": "warning", "access": "none"},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   map[string]any{"rules": rules},
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// SocksTo is the sing-box side of an Xray server.
func SocksTo(tag string, port int) map[string]any {
	return map[string]any{"type": "socks", "tag": tag, "server": "127.0.0.1", "server_port": port, "version": "5"}
}

// AssignPorts gives every tag a local port, in list order starting at
// BasePort. A tag keeps the port it has in prev, so a refresh does not move
// a server that is in use. free reports whether a port can be bound; ports
// that cannot (taken, or inside a range Windows reserved) are passed over.
func AssignPorts(tags []string, prev map[string]int, free func(port int) bool) (map[string]int, error) {
	return AssignPortsFrom(BasePort, tags, prev, free)
}

// AssignPortsFrom is AssignPorts with the first port chosen by the caller.
func AssignPortsFrom(base int, tags []string, prev map[string]int, free func(port int) bool) (map[string]int, error) {
	out := make(map[string]int, len(tags))
	taken := map[int]bool{}
	for _, t := range tags {
		if p, ok := prev[t]; ok && !taken[p] {
			out[t] = p
			taken[p] = true
		}
	}
	port := base
	for _, t := range tags {
		if _, ok := out[t]; ok {
			continue
		}
		for taken[port] || !free(port) {
			port++
			if port > 65000 {
				return nil, errors.New("no free local port for xray")
			}
		}
		out[t] = port
		taken[port] = true
		port++
	}
	return out, nil
}

// PortFree is the real test for AssignPorts: binding is the only honest
// answer on Windows.
func PortFree(port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// Test runs "xray run -test" on a config and returns Xray's complaint if it
// does not accept it.
func Test(ctx context.Context, exe, configPath, dir string) error {
	out, err := runx.Output(ctx, 20*time.Second, exe, dir, "run", "-test", "-c", configPath)
	if err != nil {
		return fmt.Errorf("xray -test: %w: %s", err, lastLine(out))
	}
	return nil
}

func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
