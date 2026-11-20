// Package clash is a client of the Clash API that sing-box serves on
// loopback. The daemon is its only user; nothing else knows the secret.
package clash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// PingURL is what a delay test fetches through a server.
const PingURL = "http://www.gstatic.com/generate_204"

// ProbeTimeout is how long one server gets to answer. The core gives its own
// checks 15 s; 3 s used to fail a server over one slow handshake. It must
// stay within int16 milliseconds: that is what sing-box parses.
const ProbeTimeout = 5 * time.Second

// defaultTimeout bounds a call whose context carries no deadline. The API is
// local, so anything slower than this means the core is stuck.
const defaultTimeout = 3 * time.Second

type Client struct {
	base   string
	secret string
	http   *http.Client
}

// New makes a client for the controller at addr (host:port).
func New(addr, secret string) *Client {
	return &Client{
		base:   "http://" + addr,
		secret: secret,
		http: &http.Client{
			Transport: &http.Transport{
				// The API is always loopback; a proxy env var must not hijack
				// it, and least of all the tunnel this very daemon is watching.
				Proxy:       nil,
				DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
			},
		},
	}
}

// Close drops idle connections to a core that is gone.
func (cl *Client) Close() { cl.http.CloseIdleConnections() }

func (cl *Client) do(ctx context.Context, timeout time.Duration, method, path string, body []byte, out any) error {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, cl.base+path, rdr)
	if err != nil {
		return err
	}
	if cl.secret != "" {
		req.Header.Set("Authorization", "Bearer "+cl.secret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cl.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Version asks the core who it is. It is the cheapest authenticated call,
// which makes it the readiness check: an answer means the core has parsed
// its config and is serving.
func (cl *Client) Version(ctx context.Context) (string, error) {
	var r struct {
		Version string `json:"version"`
	}
	err := cl.do(ctx, 0, "GET", "/version", nil, &r)
	return r.Version, err
}

// ProxyInfo is one outbound as GET /proxies reports it. The core keeps the
// last delay test of every server.
type ProxyInfo struct {
	Type   string // "Selector", "URLTest", "VLESS", ...
	Now    string // groups only: the member in use
	All    []string
	Delay  int       // ms, from the last successful test
	Tested time.Time // when that test ran; zero means the server has no result
}

func (p ProxyInfo) Group() bool { return len(p.All) > 0 }
func (p ProxyInfo) Alive() bool { return !p.Tested.IsZero() }

// Proxies reads the whole outbound table in one request: who is selected
// where, and what every server answered last. Reading it tests nothing.
func (cl *Client) Proxies(ctx context.Context) (map[string]ProxyInfo, error) {
	var r struct {
		Proxies map[string]struct {
			Type    string   `json:"type"`
			Now     string   `json:"now"`
			All     []string `json:"all"`
			History []struct {
				Time  time.Time `json:"time"`
				Delay int       `json:"delay"`
			} `json:"history"`
		} `json:"proxies"`
	}
	if err := cl.do(ctx, 0, "GET", "/proxies", nil, &r); err != nil {
		return nil, err
	}
	out := make(map[string]ProxyInfo, len(r.Proxies))
	for tag, p := range r.Proxies {
		info := ProxyInfo{Type: p.Type, Now: p.Now, All: p.All}
		if n := len(p.History); n > 0 && p.History[n-1].Delay > 0 {
			info.Delay = p.History[n-1].Delay
			info.Tested = p.History[n-1].Time
		}
		out[tag] = info
	}
	return out, nil
}

// LeafOf follows groups down to a real server. A plain server is its own
// leaf. The depth is bounded so a config with a group cycle cannot spin.
func LeafOf(px map[string]ProxyInfo, tag string) string {
	for i := 0; i < 3; i++ {
		p, ok := px[tag]
		if !ok || p.Now == "" {
			break
		}
		tag = p.Now
	}
	return tag
}

// Connections is the summary of GET /connections.
type Connections struct {
	DownTotal uint64
	UpTotal   uint64
	Total     int // open connections
	ViaVPN    int // those whose chain does not end in "direct"
}

// Traffic reads the byte counters and the open connections.
func (cl *Client) Traffic(ctx context.Context) (Connections, error) {
	var r struct {
		DownloadTotal uint64 `json:"downloadTotal"`
		UploadTotal   uint64 `json:"uploadTotal"`
		Connections   []struct {
			Chains []string `json:"chains"`
		} `json:"connections"`
	}
	if err := cl.do(ctx, 0, "GET", "/connections", nil, &r); err != nil {
		return Connections{}, err
	}
	out := Connections{DownTotal: r.DownloadTotal, UpTotal: r.UploadTotal, Total: len(r.Connections)}
	for _, c := range r.Connections {
		direct := false
		for _, ch := range c.Chains {
			if ch == "direct" {
				direct = true
				break
			}
		}
		if !direct {
			out.ViaVPN++
		}
	}
	return out, nil
}

// Delay tests one server right now. It is not a passive read: a failure
// makes the core forget the server's last result.
func (cl *Client) Delay(ctx context.Context, tag string) (int, error) {
	return cl.DelayURL(ctx, tag, PingURL, ProbeTimeout)
}

// DelayURL is Delay with the address to fetch and the time to wait chosen
// by the caller. It works for any outbound, "direct" included: that is how
// the daemon asks whether there is internet past the VPN.
func (cl *Client) DelayURL(ctx context.Context, tag, target string, timeout time.Duration) (int, error) {
	var r struct {
		Delay int `json:"delay"`
	}
	p := fmt.Sprintf("/proxies/%s/delay?timeout=%d&url=%s",
		url.PathEscape(tag), timeout.Milliseconds(), url.QueryEscape(target))
	err := cl.do(ctx, timeout+5*time.Second, "GET", p, nil, &r)
	return r.Delay, err
}

// Unreachable tells a call that found nobody listening (the core is gone or
// not up yet) from one the core answered with an error, such as a server
// that did not answer a delay test.
//
// There is deliberately no group test here. sing-box's /group/{tag}/delay
// gives its timeout to the whole group, not to each member, and tests ten at
// a time: with a few dead servers holding their slots, the members at the end
// of a long list are never tried and come back as silent.
func Unreachable(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// Mode reads the routing mode and the modes this core knows. A core started
// from a config without mode rules lists only "Rule".
func (cl *Client) Mode(ctx context.Context) (cur string, list []string, err error) {
	var r struct {
		Mode     string   `json:"mode"`
		ModeList []string `json:"mode-list"`
	}
	err = cl.do(ctx, 0, "GET", "/configs", nil, &r)
	return r.Mode, r.ModeList, err
}

// SetMode switches the mode. sing-box answers 204 even to a mode it does not
// know and just keeps the old one, so callers check the mode list first.
func (cl *Client) SetMode(ctx context.Context, mode string) error {
	body, _ := json.Marshal(map[string]string{"mode": mode})
	return cl.do(ctx, 5*time.Second, "PATCH", "/configs", body, nil)
}

// CloseAll drops open connections: they were routed under the old mode or
// server and would keep that route until they close on their own.
func (cl *Client) CloseAll(ctx context.Context) error {
	return cl.do(ctx, 5*time.Second, "DELETE", "/connections", nil, nil)
}

// SwitchTo points a selector at one of its members.
func (cl *Client) SwitchTo(ctx context.Context, selector, tag string) error {
	body, _ := json.Marshal(map[string]string{"name": tag})
	return cl.do(ctx, 5*time.Second, "PUT", "/proxies/"+url.PathEscape(selector), body, nil)
}

// Conn is one open connection as GET /connections reports it.
type Conn struct {
	ID          string
	Host        string
	DestIP      string
	DestPort    string
	ProcessPath string
	Chains      []string // from the server actually used up to the group
	Upload      int64
	Download    int64
	Start       string
}

// Direct reports whether the connection left without the VPN.
func (c Conn) Direct() bool {
	for _, ch := range c.Chains {
		if ch == "direct" {
			return true
		}
	}
	return false
}

// Snapshot is the byte counters and every open connection, in one request.
type Snapshot struct {
	UpTotal   int64
	DownTotal int64
	Conns     []Conn
}

// Snapshot reads GET /connections in full.
func (cl *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	var r struct {
		DownloadTotal int64 `json:"downloadTotal"`
		UploadTotal   int64 `json:"uploadTotal"`
		Connections   []struct {
			ID       string   `json:"id"`
			Chains   []string `json:"chains"`
			Upload   int64    `json:"upload"`
			Download int64    `json:"download"`
			Start    string   `json:"start"`
			Metadata struct {
				Host        string `json:"host"`
				DstIP       string `json:"destinationIP"`
				DstPort     string `json:"destinationPort"`
				ProcessPath string `json:"processPath"`
			} `json:"metadata"`
		} `json:"connections"`
	}
	if err := cl.do(ctx, 0, "GET", "/connections", nil, &r); err != nil {
		return Snapshot{}, err
	}
	out := Snapshot{UpTotal: r.UploadTotal, DownTotal: r.DownloadTotal, Conns: make([]Conn, 0, len(r.Connections))}
	for _, c := range r.Connections {
		out.Conns = append(out.Conns, Conn{
			ID: c.ID, Host: c.Metadata.Host, DestIP: c.Metadata.DstIP, DestPort: c.Metadata.DstPort,
			ProcessPath: c.Metadata.ProcessPath, Chains: c.Chains,
			Upload: c.Upload, Download: c.Download, Start: c.Start,
		})
	}
	return out, nil
}
