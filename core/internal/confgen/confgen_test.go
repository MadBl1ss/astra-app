package confgen

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"strelka/internal/rules"
	"strelka/internal/subs"
	"strelka/internal/testjson"
	"strelka/internal/xray"
	"strelka/proto"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// The servers of every golden config are made up.
var testLinks = []string{
	"vless://11111111-1111-4111-8111-111111111111@de.example.com:443?security=tls&sni=de.example.com&type=ws&path=%2Fws#%F0%9F%87%A9%F0%9F%87%AA%20Germany",
	"trojan://made-up-password@nl.example.com:443?sni=nl.example.com#%F0%9F%87%B3%F0%9F%87%B1%20Netherlands",
	"ss://2022-blake3-aes-128-gcm:bWFkZS11cC1rZXktMTIzNA%3D%3D@fi.example.com:8388#%F0%9F%87%AB%F0%9F%87%AE%20Finland",
	"hysteria2://made-up@pl.example.com:8443/?sni=pl.example.com#%F0%9F%87%B5%F0%9F%87%B1%20Poland",
	"vless://22222222-2222-4222-8222-222222222222@se.example.com:443?security=reality&sni=www.example.org&pbk=TWFkZVVwUHVibGljS2V5TWFkZVVwUHVibGljS2V5MDA&sid=ab12&flow=xtls-rprx-vision#%F0%9F%87%B8%F0%9F%87%AA%20Sweden",
	"vless://33333333-3333-4333-8333-333333333333@lv.example.com:443?security=tls&sni=lv.example.com&type=xhttp&path=%2Fx#%F0%9F%87%B1%F0%9F%87%BB%20Latvia",
}

// servers parses the test links like the profile would and gives the Xray
// ones their ports.
func servers(t *testing.T, useXray bool, only ...string) ([]Server, []xray.Server) {
	t.Helper()
	parsed, _ := subs.ParseLinks(testLinks, "t", subs.ReservedTags(), useXray)
	var out []Server
	var xs []xray.Server
	port := xray.BasePort
	for _, s := range parsed {
		if len(only) > 0 && !contains(only, s.ID) {
			continue
		}
		cs := Server{ID: s.ID, Outbound: s.Outbound}
		if s.Via == proto.ViaXray {
			cs.XrayPort = port
			xs = append(xs, xray.Server{Tag: s.ID, Port: port, Outbound: s.Xray})
			port++
		}
		out = append(out, cs)
	}
	if len(out) == 0 {
		t.Fatal("ни одна тестовая ссылка не разобралась")
	}
	return out, xs
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Fixed patterns keep the golden files the same on every OS.
const (
	testXrayMatch = `(?i)^C:\\Program Files\\Strelka\\xray\.exe$`
	testSelfMatch = `(?i)^C:\\Program Files\\Strelka\\strelkad\.exe$`
)

func base(t *testing.T, useXray bool, only ...string) (Input, []xray.Server) {
	srv, xs := servers(t, useXray, only...)
	return Input{
		Servers:     srv,
		Mode:        proto.ModeRule,
		IPv6:        "tunnel",
		StrictRoute: true,
		XrayMatch:   testXrayMatch,
		SelfMatch:   testSelfMatch,
		SelfHosts:   []string{"ip-api.com"},
		Controller:  "127.0.0.1:9090",
		Secret:      "made-up-secret",
	}, xs
}

// The daemon's own request about the place goes past the VPN even in
// "all traffic" mode, and only that request: the rule needs the daemon's
// path and the service's name at once.
func TestSelfRuleSendsOnlyTheDaemonDirect(t *testing.T) {
	in, _ := base(t, false, "t-DE")
	in.Mode = proto.ModeGlobal
	raw, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	rules := testjson.Get(raw, "route.rules")
	self := strings.Index(rules, `strelkad`)
	glob := strings.Index(rules, "clash_mode")
	if self < 0 || glob < 0 || self > glob {
		t.Fatalf("правило службы должно стоять раньше «весь трафик»: %s", rules)
	}
	rule := rules[strings.LastIndex(rules[:self], "{"):]
	rule = rule[:strings.Index(rule, "}")+1]
	if !strings.Contains(rule, `"domain":["ip-api.com"]`) || !strings.Contains(rule, `"outbound":"direct"`) {
		t.Errorf("правило службы — путь и имя сервиса сразу, напрямую: %s", rule)
	}

	for _, drop := range []func(*Input){
		func(in *Input) { in.SelfMatch = "" },
		func(in *Input) { in.SelfHosts = nil },
	} {
		in, _ := base(t, false, "t-DE")
		drop(&in)
		raw, err := Build(in)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(testjson.Get(raw, "route.rules"), "strelkad") || strings.Contains(string(raw), `"domain":[]`) {
			t.Errorf("без пути или без имени правила быть не должно: %s", testjson.Get(raw, "route.rules"))
		}
	}
}

type goldenCase struct {
	name string
	in   Input
	xray []xray.Server
}

func goldenCases(t *testing.T) []goldenCase {
	one, _ := base(t, false, "t-DE")

	withXray, xs := base(t, true)
	withXray.Selected = "t-SE"

	global, _ := base(t, false)
	global.Mode = proto.ModeGlobal
	global.Selected = "t-NL"

	localProxy, _ := base(t, false, "t-DE", "t-NL")
	localProxy.LocalProxyPort = 2080

	noTUN, _ := base(t, false, "t-DE", "t-NL")
	noTUN.NoTUN = true

	v4only, _ := base(t, false, "t-DE")
	v4only.IPv6 = "block"
	v4only.StrictRoute = false

	return []goldenCase{
		{"one_server", one, nil},
		{"xray_servers", withXray, xs},
		{"global_mode", global, nil},
		{"local_proxy", localProxy, nil},
		{"no_tun", noTUN, nil},
		{"ipv6_block", v4only, nil},
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("нет эталона %s (создать: go test ./internal/confgen -update): %v", name, err)
	}
	// Git may hand the file out with CRLF on Windows.
	if strings.ReplaceAll(string(want), "\r\n", "\n") != string(got) {
		t.Errorf("%s расходится с эталоном. Если так и задумано: go test ./internal/confgen -update\n%s", name, got)
	}
}

func TestGoldenConfigs(t *testing.T) {
	for _, c := range goldenCases(t) {
		got, err := Build(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		golden(t, c.name+".json", got)
		if c.xray != nil {
			xcfg, err := xray.Build(c.xray)
			if err != nil {
				t.Fatalf("%s: xray: %v", c.name, err)
			}
			golden(t, c.name+".xray.json", xcfg)
		}
	}
}

func TestEmptyProfileHasNoConfig(t *testing.T) {
	in, _ := base(t, false)
	in.Servers = nil
	if _, err := Build(in); !errors.Is(err, ErrNoServers) {
		t.Fatalf("пустой профиль: %v", err)
	}
}

// What the architecture promises about the template, checked on the output
// rather than trusted to the golden files.
func TestBuildKeepsThePromises(t *testing.T) {
	in, _ := base(t, true)
	in.Selected = "t-NL"
	in.Mode = proto.ModeGlobal
	raw, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}

	sel := testjson.Find(raw, "outbounds", "tag", SelectorTag)
	if testjson.Get(sel, "type") != "selector" || testjson.Get(sel, "default") != "t-NL" ||
		testjson.Get(sel, "interrupt_exist_connections") != "true" {
		t.Errorf("селектор: %s", sel)
	}
	members := testjson.Get(sel, "outbounds")
	if strings.Contains(members, `"direct"`) || strings.Contains(members, `"auto"`) {
		t.Errorf("в селекторе не должно быть direct и auto: %s", members)
	}
	if testjson.Find(raw, "outbounds", "type", "urltest") != nil {
		t.Error("группы urltest быть не должно")
	}
	for _, s := range in.Servers {
		ob := testjson.Find(raw, "outbounds", "tag", s.ID)
		if ob == nil || !strings.Contains(members, `"`+s.ID+`"`) {
			t.Errorf("сервер %s не попал в конфиг или в селектор", s.ID)
		}
		if s.XrayPort > 0 && (testjson.Get(ob, "type") != "socks" || testjson.Get(ob, "server") != "127.0.0.1" ||
			testjson.Get(ob, "server_port") == "0") {
			t.Errorf("сервер через Xray должен быть socks на локальный порт: %s", ob)
		}
	}

	tun := testjson.Find(raw, "inbounds", "type", "tun")
	if testjson.Get(tun, "interface_name") != TUNName || testjson.Get(tun, "strict_route") != "true" ||
		testjson.Get(tun, "address.1") == "" || testjson.Get(tun, "mtu") != "9000" {
		t.Errorf("TUN: %s", tun)
	}
	if testjson.Find(raw, "inbounds", "type", "mixed") != nil {
		t.Error("локального прокси быть не должно, пока он не включён")
	}
	if testjson.Get(raw, "log.output") != "" {
		t.Error("лог движка должен идти в stderr")
	}
	if testjson.Get(raw, "experimental.clash_api.external_ui") != "" {
		t.Error("веб-панели быть не должно")
	}
	if testjson.Get(raw, "experimental.clash_api.external_controller") != "127.0.0.1:9090" ||
		testjson.Get(raw, "experimental.clash_api.secret") != "made-up-secret" ||
		testjson.Get(raw, "experimental.clash_api.default_mode") != ClashGlobal {
		t.Errorf("clash_api: %s", testjson.Get(raw, "experimental.clash_api"))
	}
	if strings.Contains(string(raw), "_strelka") {
		t.Error("служебные пометки шаблона попали в конфиг")
	}
	// The helper is exempted by full path; a bare name anyone could take.
	if strings.Contains(string(raw), `"process_name"`) || !strings.Contains(testjson.Get(raw, "route.rules"), "Strelka") {
		t.Errorf("правило для xray должно быть по полному пути: %s", testjson.Get(raw, "route.rules"))
	}
	// Order matters: LAN and the direct list win over "all traffic".
	order := testjson.Get(raw, "route.rules")
	private, direct, glob, vpn := strings.Index(order, "ip_is_private"), strings.Index(order, `"rule_set":"direct"`),
		strings.Index(order, "clash_mode"), strings.Index(order, `"rule_set":"vpn"`)
	if !(private >= 0 && private < direct && direct < glob && glob < vpn) {
		t.Errorf("порядок правил: private=%d direct=%d global=%d vpn=%d", private, direct, glob, vpn)
	}

	// A selection that is gone falls back to the first server instead of
	// producing a config the engine rejects.
	in.Selected = "gone"
	raw, _ = Build(in)
	if got := testjson.Get(testjson.Find(raw, "outbounds", "tag", SelectorTag), "default"); got != in.Servers[0].ID {
		t.Errorf("пропавший выбор: default=%q", got)
	}
}

func TestBuildVariants(t *testing.T) {
	in, _ := base(t, false, "t-DE")
	in.IPv6 = "block"
	in.StrictRoute = false
	in.XrayMatch = ""
	raw, _ := Build(in)
	tun := testjson.Find(raw, "inbounds", "type", "tun")
	if testjson.Get(tun, "address") != `["172.19.0.1/30"]` || testjson.Get(tun, "strict_route") != "false" {
		t.Errorf("IPv6 закрыт, strict_route выключен: %s", tun)
	}
	if strings.Contains(testjson.Get(raw, "route.rules"), "xray") {
		t.Error("без Xray правило для него не нужно")
	}

	in.LocalProxyPort = 2080
	raw, _ = Build(in)
	mixed := testjson.Find(raw, "inbounds", "type", "mixed")
	if testjson.Get(mixed, "listen") != "127.0.0.1" || testjson.Get(mixed, "listen_port") != "2080" {
		t.Errorf("локальный прокси: %s", mixed)
	}
	if !strings.Contains(testjson.Get(raw, "route.rules"), `"inbound":"mixed-in"`) {
		t.Error("нет правила для локального прокси")
	}

	in.LocalProxyPort = 0
	in.NoTUN = true
	raw, _ = Build(in)
	if testjson.Find(raw, "inbounds", "type", "tun") != nil {
		t.Error("в режиме без TUN остался TUN")
	}
	if got := testjson.Get(testjson.Find(raw, "inbounds", "type", "mixed"), "listen_port"); got != "20808" {
		t.Errorf("в режиме без TUN нужен локальный прокси на порту по умолчанию, а там %q", got)
	}
}

func TestExactPath(t *testing.T) {
	if ExactPath("") != "" {
		t.Error("пустой путь — пустой шаблон")
	}
	re := ExactPath(`C:\Program Files (x86)\Strelka\xray.exe`)
	if !strings.HasSuffix(re, `^C:\\Program Files \(x86\)\\Strelka\\xray\.exe$`) {
		t.Errorf("путь не экранирован: %s", re)
	}
	if (runtime.GOOS == "windows") != strings.HasPrefix(re, "(?i)") {
		t.Errorf("регистр игнорируется только на Windows: %s", re)
	}
}

// enginesDir finds the development engines folder at the repository root.
func enginesDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "engines"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func exe(dir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

// Every golden config goes through the real engine's own check. Skipped
// when the engines are not there (scripts/fetch-engines.ps1 brings them).
func TestGoldenConfigsPassEngineCheck(t *testing.T) {
	box := exe(enginesDir(t), "sing-box")
	if _, err := os.Stat(box); err != nil {
		t.Skipf("нет %s: проверка движком пропущена", box)
	}
	xrayExe := exe(enginesDir(t), "xray")
	_, xrayErr := os.Stat(xrayExe)

	for _, c := range goldenCases(t) {
		data := t.TempDir()
		rulesDir := filepath.Join(data, "rules")
		if _, err := rules.Ensure(rulesDir); err != nil {
			t.Fatal(err)
		}
		if err := WriteAutoLists(rulesDir); err != nil {
			t.Fatal(err)
		}
		raw, err := Build(c.in)
		if err != nil {
			t.Fatal(err)
		}
		cfg := filepath.Join(data, "config.json")
		if err := os.WriteFile(cfg, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := Check(context.Background(), box, cfg, data); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}

		if c.xray == nil {
			continue
		}
		if xrayErr != nil {
			t.Logf("нет %s: проверка xray.json пропущена", xrayExe)
			continue
		}
		xcfg, _ := xray.Build(c.xray)
		xpath := filepath.Join(data, "xray.json")
		if err := os.WriteFile(xpath, xcfg, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := xray.Test(context.Background(), xrayExe, xpath, data); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}

	// And the check does reject a config the engine cannot read, so a pass
	// means something. It reads the config without starting anything: a
	// field it does not know is caught, a selector naming a missing server
	// is not (that surfaces only at start).
	data := t.TempDir()
	bad := filepath.Join(data, "config.json")
	os.WriteFile(bad, []byte(`{"outbounds":[{"type":"vless","tag":"x","no_such_field":1}]}`), 0o600)
	if err := Check(context.Background(), box, bad, data); err == nil {
		t.Error("sing-box check пропустил конфиг с неизвестным полем")
	} else {
		t.Logf("отказ движка: %v", err)
	}
}

func TestAutoListsWithTheEngine(t *testing.T) {
	box := exe(enginesDir(t), "sing-box")
	if _, err := os.Stat(box); err != nil {
		t.Skipf("нет %s: проверка движком пропущена", box)
	}
	dir := t.TempDir()
	if err := WriteAutoLists(dir); err != nil {
		t.Fatal(err)
	}
	// A second call leaves existing files alone.
	if err := WriteAutoLists(dir); err != nil {
		t.Fatal(err)
	}
	ai := AutoListFile(dir, "itdog-google-ai")
	if hit, err := MatchAutoList(context.Background(), box, ai, "gemini.google.com"); err != nil || !hit {
		t.Errorf("gemini.google.com должен быть в списке: %v %v", hit, err)
	}
	if hit, err := MatchAutoList(context.Background(), box, ai, "example.org"); err != nil || hit {
		t.Errorf("example.org не должен быть в списке: %v %v", hit, err)
	}
	if n, err := CountAutoList(context.Background(), box, ai); err != nil || n < 10 {
		t.Errorf("записей в списке: %d, %v", n, err)
	}
}
