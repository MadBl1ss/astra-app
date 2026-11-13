package clash

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// What sing-box 1.14 answers on GET /proxies, trimmed: a selector on a
// group, the group on a-DE, one server with a result, one without.
const proxiesReply = `{"proxies":{
  "GLOBAL":{"type":"Fallback","name":"GLOBAL","udp":true,"history":[],"all":["proxy","auto","a-DE","a-NL"],"now":"proxy"},
  "proxy":{"type":"Selector","name":"proxy","udp":true,"history":[{"time":"2026-11-11T10:00:00.5+03:00","delay":84}],"now":"auto","all":["auto","a-DE","a-NL"]},
  "auto":{"type":"URLTest","name":"auto","udp":true,"history":[{"time":"2026-11-11T10:00:00.5+03:00","delay":84}],"now":"a-DE","all":["a-DE","a-NL"]},
  "a-DE":{"type":"VLESS","name":"a-DE","udp":true,"history":[{"time":"2026-11-11T10:00:00.5+03:00","delay":84}]},
  "a-NL":{"type":"VLESS","name":"a-NL","udp":true,"history":[]},
  "direct":{"type":"Direct","name":"direct","udp":true,"history":[]}
}}`

func apiStub(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cl := New(strings.TrimPrefix(srv.URL, "http://"), "s3cret")
	t.Cleanup(cl.Close)
	return cl
}

var ctx = context.Background()

func TestVersionSendsTheSecret(t *testing.T) {
	cl := apiStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" || r.Header.Get("Authorization") != "Bearer s3cret" {
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"meta":true,"premium":true,"version":"sing-box 1.14.0"}`)
	})
	v, err := cl.Version(ctx)
	if err != nil || v != "sing-box 1.14.0" {
		t.Errorf("version: %q, %v", v, err)
	}

	wrong := New(strings.TrimPrefix(cl.base, "http://"), "чужой")
	defer wrong.Close()
	if _, err := wrong.Version(ctx); err == nil {
		t.Error("с чужим секретом версия не должна читаться")
	}
}

func TestVersionRespectsTheContext(t *testing.T) {
	release := make(chan struct{})
	cl := apiStub(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	c, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := cl.Version(c); err == nil {
		t.Error("зависший движок должен дать ошибку")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("отмена контекста не сработала: ждал %v", d)
	}
}

func TestProxiesReadsSelectionAndResults(t *testing.T) {
	cl := apiStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxies" || r.Header.Get("Authorization") != "Bearer s3cret" {
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, proxiesReply)
	})
	px, err := cl.Proxies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if px["proxy"].Now != "auto" || !px["auto"].Group() || px["a-DE"].Group() {
		t.Errorf("группы разобраны неверно: proxy=%+v auto=%+v", px["proxy"], px["auto"])
	}
	if leaf := LeafOf(px, px["proxy"].Now); leaf != "a-DE" {
		t.Errorf("сервер под auto: %q, жду a-DE", leaf)
	}
	if LeafOf(px, "a-NL") != "a-NL" || LeafOf(px, "нет такого") != "нет такого" {
		t.Error("обычный сервер должен быть сам себе листом")
	}
	de := px["a-DE"]
	if !de.Alive() || de.Delay != 84 || de.Tested.IsZero() {
		t.Errorf("a-DE: %+v, жду ответ 84 мс", de)
	}
	if px["a-NL"].Alive() {
		t.Errorf("a-NL без истории считается живым: %+v", px["a-NL"])
	}
}

// The probe must give a server longer than the 3 s that used to fail it, and
// stay inside what sing-box parses: the per-proxy timeout is an int16.
func TestProbeAndGroupTestRequests(t *testing.T) {
	var seen []string
	cl := apiStub(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+"?timeout="+r.URL.Query().Get("timeout"))
		fmt.Fprint(w, `{"delay":120}`)
	})
	ms, err := cl.Delay(ctx, "a-DE")
	if err != nil || ms != 120 {
		t.Fatalf("delay: %d, %v", ms, err)
	}
	if err := cl.TestGroup(ctx, "proxy", 10*time.Second); err != nil {
		t.Fatalf("testGroup: %v", err)
	}
	want := []string{"/proxies/a-DE/delay?timeout=5000", "/group/proxy/delay?timeout=10000"}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Errorf("запросы:\n есть %v\n жду  %v", seen, want)
	}
	if ProbeTimeout <= 3*time.Second || ProbeTimeout.Milliseconds() > 32767 {
		t.Errorf("ProbeTimeout %v: нужен больше 3 с и в пределах int16 мс", ProbeTimeout)
	}
}

func TestTrafficCountsWhatGoesThroughVPN(t *testing.T) {
	cl := apiStub(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"downloadTotal":900,"uploadTotal":100,"connections":[
			{"chains":["a-DE","proxy"]},{"chains":["direct"]},{"chains":["a-NL","auto","proxy"]}]}`)
	})
	got, err := cl.Traffic(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.DownTotal != 900 || got.UpTotal != 100 || got.Total != 3 || got.ViaVPN != 2 {
		t.Errorf("трафик: %+v", got)
	}
}

func TestModeAndWrites(t *testing.T) {
	type call struct{ method, path, body string }
	var seen []call
	cl := apiStub(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, call{r.Method, r.URL.Path, string(body)})
		if r.Method == "GET" {
			fmt.Fprint(w, `{"mode":"Rule","mode-list":["Rule","Global"]}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	cur, list, err := cl.Mode(ctx)
	if err != nil || cur != "Rule" || len(list) != 2 {
		t.Fatalf("mode: %q %v %v", cur, list, err)
	}
	if err := cl.SetMode(ctx, "Global"); err != nil {
		t.Fatal(err)
	}
	if err := cl.CloseAll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cl.SwitchTo(ctx, "proxy", "Нидерланды 1"); err != nil {
		t.Fatal(err)
	}
	want := []call{
		{"GET", "/configs", ""},
		{"PATCH", "/configs", `{"mode":"Global"}`},
		{"DELETE", "/connections", ""},
		{"PUT", "/proxies/proxy", `{"name":"Нидерланды 1"}`},
	}
	if len(seen) != len(want) {
		t.Fatalf("запросов %d, жду %d: %v", len(seen), len(want), seen)
	}
	for i := range want {
		if seen[i].method != want[i].method || seen[i].path != want[i].path || !sameJSON(seen[i].body, want[i].body) {
			t.Errorf("запрос %d: %+v, жду %+v", i, seen[i], want[i])
		}
	}
}

func TestErrorStatusIsAnError(t *testing.T) {
	cl := apiStub(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	})
	if err := cl.SwitchTo(ctx, "proxy", "a-DE"); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("жду ошибку с кодом 503, получил %v", err)
	}
}

func sameJSON(a, b string) bool {
	if a == b {
		return true
	}
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	return fmt.Sprint(x) == fmt.Sprint(y)
}
