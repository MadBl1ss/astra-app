package confgen

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"strelka/internal/clash"
)

// The test's own engine takes no traffic and shares nothing with the VPN's:
// no inbound, no cache file, no rule set, the servers behind the selector
// the Clash API tests them through.
func TestProbeConfigTakesNoTraffic(t *testing.T) {
	srv, _ := servers(t, true)
	raw, err := BuildProbe(srv, "127.0.0.1:9090", "made-up-secret")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Inbounds  []any `json:"inbounds"`
		Outbounds []struct {
			Type       string   `json:"type"`
			Tag        string   `json:"tag"`
			Outbounds  []string `json:"outbounds"`
			Server     string   `json:"server"`
			ServerPort int      `json:"server_port"`
		} `json:"outbounds"`
		Route struct {
			Final   string `json:"final"`
			RuleSet []any  `json:"rule_set"`
			Rules   []any  `json:"rules"`
		} `json:"route"`
		Experimental map[string]json.RawMessage `json:"experimental"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Inbounds) != 0 || len(cfg.Route.Rules) != 0 || len(cfg.Route.RuleSet) != 0 || cfg.Route.Final != DirectTag {
		t.Errorf("проверочный движок принимает трафик или маршрутизирует: %s", raw)
	}
	if _, ok := cfg.Experimental["cache_file"]; ok {
		t.Error("у проверочного движка свой cache.db: он делил бы файл с VPN")
	}
	sel := cfg.Outbounds[0]
	if sel.Type != "selector" || sel.Tag != SelectorTag || len(sel.Outbounds) != len(srv) {
		t.Errorf("первым должен идти селектор над всеми серверами: %+v", sel)
	}
	socks := 0
	for _, ob := range cfg.Outbounds {
		if ob.Type == "socks" {
			socks++
			if ob.Server != "127.0.0.1" || ob.ServerPort == 0 {
				t.Errorf("сервер Xray не смотрит на свой порт: %+v", ob)
			}
		}
	}
	if socks == 0 {
		t.Error("в тестовом наборе есть серверы Xray, а в конфиге их нет")
	}
	if _, err := BuildProbe(nil, "127.0.0.1:9090", "x"); err != ErrNoServers {
		t.Errorf("пустой список: %v", err)
	}
}

// The real engine accepts the config and, started from it, serves the API
// the daemon tests servers through. Skipped without the engines.
func TestProbeConfigRunsInTheEngine(t *testing.T) {
	box := exe(enginesDir(t), "sing-box")
	if _, err := os.Stat(box); err != nil {
		t.Skipf("нет %s: проверка движком пропущена", box)
	}
	srv, _ := servers(t, true)
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	raw, err := BuildProbe(srv, addr, "made-up-secret")
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	cfg := filepath.Join(data, "probe.json")
	if err := os.WriteFile(cfg, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Check(context.Background(), box, cfg, data); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(box, "run", "-c", cfg, "-D", data, "--disable-color")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	api := clash.New(addr, "made-up-secret")
	deadline := time.Now().Add(10 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := api.Version(ctx)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("движок с проверочным конфигом не ответил: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	px, err := api.Proxies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range srv {
		if _, ok := px[s.ID]; !ok {
			t.Errorf("движок не знает сервер %s", s.ID)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
