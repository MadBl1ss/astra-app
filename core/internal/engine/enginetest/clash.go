package enginetest

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// fakeClash is the part of the Clash API the daemon uses, over the selector
// found in the config the fake was started with. A server whose tag contains
// "dead" never answers a delay test; every other answers in a time derived
// from its tag, so tests can tell servers apart. Both can be overridden with
// a script of answers, see /fake/script.
type fakeClash struct {
	mu      sync.Mutex
	members []string
	now     string
	mode    string
	tested  map[string]probe
	closed  int // how many times DELETE /connections was called

	scripts map[string][]string // per tag: answers to the next delay tests
	asked   map[string]int      // per tag: delay tests so far
	flowing bool                // the VPN connection receives bytes
	rx      int64
}

type probe struct {
	Time  time.Time `json:"time"`
	Delay int       `json:"delay"`
}

// DelayOf is the delay the fake reports for a server.
func DelayOf(tag string) int { return 40 + 7*len(tag) }

func newFakeClash() *fakeClash {
	fc := &fakeClash{mode: "Rule", tested: map[string]probe{}, scripts: map[string][]string{}, asked: map[string]int{}}
	var cfg struct {
		Outbounds []struct {
			Type      string   `json:"type"`
			Tag       string   `json:"tag"`
			Outbounds []string `json:"outbounds"`
			Default   string   `json:"default"`
		} `json:"outbounds"`
		Experimental struct {
			ClashAPI struct {
				DefaultMode string `json:"default_mode"`
			} `json:"clash_api"`
		} `json:"experimental"`
	}
	for i, a := range os.Args {
		if a == "-c" && i+1 < len(os.Args) {
			if raw, err := os.ReadFile(os.Args[i+1]); err == nil {
				json.Unmarshal(raw, &cfg)
			}
		}
	}
	for _, ob := range cfg.Outbounds {
		if ob.Type == "selector" {
			fc.members, fc.now = ob.Outbounds, ob.Default
			if fc.now == "" && len(ob.Outbounds) > 0 {
				fc.now = ob.Outbounds[0]
			}
		}
	}
	if m := cfg.Experimental.ClashAPI.DefaultMode; m != "" {
		fc.mode = m
	}
	return fc
}

func (fc *fakeClash) member(tag string) bool {
	for _, m := range fc.members {
		if m == tag {
			return true
		}
	}
	return false
}

// test runs a delay test of one server. Called with the lock held.
func (fc *fakeClash) test(tag string) (int, bool) {
	fc.asked[tag]++
	fail := strings.Contains(tag, "dead")
	if s := fc.scripts[tag]; len(s) > 0 {
		fail = s[0] == "fail"
		if len(s) > 1 { // the last answer repeats
			fc.scripts[tag] = s[1:]
		}
	}
	if fail {
		delete(fc.tested, tag) // a failed test forgets the last result
		return 0, false
	}
	p := probe{Time: time.Now(), Delay: DelayOf(tag)}
	fc.tested[tag] = p
	return p.Delay, true
}

func (fc *fakeClash) register(mux *http.ServeMux, secret string) {
	handle := func(pattern string, h func(w http.ResponseWriter, r *http.Request)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if secret != "" && r.Header.Get("Authorization") != "Bearer "+secret {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			fc.mu.Lock()
			defer fc.mu.Unlock()
			h(w, r)
		})
	}
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}

	handle("GET /proxies", func(w http.ResponseWriter, _ *http.Request) {
		px := map[string]any{
			"proxy": map[string]any{"type": "Selector", "now": fc.now, "all": fc.members, "history": []probe{}},
		}
		for _, m := range fc.members {
			hist := []probe{}
			if p, ok := fc.tested[m]; ok {
				hist = append(hist, p)
			}
			px[m] = map[string]any{"type": "VLESS", "history": hist}
		}
		reply(w, map[string]any{"proxies": px})
	})
	handle("PUT /proxies/{group}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if r.PathValue("group") != "proxy" || !fc.member(body.Name) {
			http.Error(w, "not found", http.StatusBadRequest)
			return
		}
		fc.now = body.Name
		w.WriteHeader(http.StatusNoContent)
	})
	handle("GET /proxies/{tag}/delay", func(w http.ResponseWriter, r *http.Request) {
		tag := r.PathValue("tag")
		// "direct" is an outbound like any other: the daemon tests it to
		// learn whether there is internet past the VPN.
		if !fc.member(tag) && tag != "direct" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		d, ok := fc.test(tag)
		if !ok {
			http.Error(w, `{"message":"timeout"}`, http.StatusGatewayTimeout)
			return
		}
		reply(w, map[string]int{"delay": d})
	})
	handle("GET /group/{group}/delay", func(w http.ResponseWriter, _ *http.Request) {
		out := map[string]int{}
		for _, m := range fc.members {
			if d, ok := fc.test(m); ok {
				out[m] = d
			}
		}
		reply(w, out)
	})
	handle("GET /configs", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"mode": fc.mode, "mode-list": []string{"Rule", "Global"}})
	})
	handle("PATCH /configs", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Mode string `json:"mode"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Mode == "Rule" || body.Mode == "Global" {
			fc.mode = body.Mode
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handle("GET /connections", func(w http.ResponseWriter, _ *http.Request) {
		if fc.flowing {
			fc.rx += 1000
		}
		reply(w, map[string]any{
			"downloadTotal": 2048 + fc.rx, "uploadTotal": 1024,
			"connections": []any{
				map[string]any{
					"id": "c1", "chains": []string{fc.now, "proxy"}, "upload": 10, "download": 20 + fc.rx,
					"start":    "2026-11-13T10:00:00Z",
					"metadata": map[string]string{"host": "example.com", "destinationIP": "192.0.2.10", "destinationPort": "443", "processPath": "/apps/Browser.exe"},
				},
				map[string]any{
					"id": "c2", "chains": []string{"direct"}, "upload": 1, "download": 2,
					"start":    "2026-11-13T10:00:01Z",
					"metadata": map[string]string{"host": "", "destinationIP": "192.0.2.20", "destinationPort": "80", "processPath": ""},
				},
			},
		})
	})
	handle("DELETE /connections", func(w http.ResponseWriter, _ *http.Request) {
		fc.closed++
		w.WriteHeader(http.StatusNoContent)
	})
	// Test control: what the engine was last told.
	handle("GET /fake/state", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"now": fc.now, "mode": fc.mode, "closed": fc.closed, "members": fc.members, "asked": fc.asked})
	})
	// Test control: /fake/script?tag=t-DE&answers=fail,fail,ok sets what the
	// next delay tests of a server (or of "direct") answer. The last answer
	// repeats, so "fail" alone is a dead server and "ok" a revived one.
	handle("GET /fake/script", func(w http.ResponseWriter, r *http.Request) {
		fc.scripts[r.URL.Query().Get("tag")] = strings.Split(r.URL.Query().Get("answers"), ",")
		w.WriteHeader(http.StatusNoContent)
	})
	// Test control: /fake/traffic?on=1 makes the VPN connection receive
	// bytes from now on, on=0 stops it.
	handle("GET /fake/traffic", func(w http.ResponseWriter, r *http.Request) {
		fc.flowing = r.URL.Query().Get("on") == "1"
		w.WriteHeader(http.StatusNoContent)
	})
}
