package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"strelka/internal/engine"
	"strelka/internal/engine/enginetest"
	"strelka/internal/netmon"
	"strelka/internal/rules"
	"strelka/internal/testjson"
	"strelka/proto"
)

// subServer is a made-up subscription provider.
type subServer struct {
	*httptest.Server
	mu     sync.Mutex
	links  []string
	status int
	hwid   string // the x-hwid of the last request
	hits   int
	// info is the subscription-userinfo header; the default is a plan with
	// years to go and traffic to spare.
	info string
}

func newSubServer(t *testing.T, links ...string) *subServer {
	s := &subServer{links: links, info: "upload=1; download=2; total=100; expire=1893456000"}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.hits++
		s.hwid = r.Header.Get("x-hwid")
		if s.status != 0 {
			w.WriteHeader(s.status)
			return
		}
		w.Header().Set("subscription-userinfo", s.info)
		fmt.Fprint(w, strings.Join(s.links, "\n"))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *subServer) set(status int, links ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
	if links != nil {
		s.links = links
	}
}

func (s *subServer) setInfo(info string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info = info
}

func (s *subServer) lastHWID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hwid
}

func link(label, host string) string {
	return "vless://11111111-1111-4111-8111-111111111111@" + host + ":443?security=tls#[" + label + "] made up"
}

// fake asks the fake engine what it was last told.
func (r *rig) fake() (st struct {
	Now     string   `json:"now"`
	Mode    string   `json:"mode"`
	Closed  int      `json:"closed"`
	Members []string `json:"members"`
}) {
	r.t.Helper()
	cfg, err := os.ReadFile(r.paths.RunConfig())
	if err != nil {
		r.t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "http://"+testjson.Get(cfg, "experimental.clash_api.external_controller")+"/fake/state", nil)
	req.Header.Set("Authorization", "Bearer "+testjson.Get(cfg, "experimental.clash_api.secret"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatalf("поддельный движок не отвечает: %v", err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		r.t.Fatal(err)
	}
	return st
}

// eventually polls the fake engine: the daemon applies changes to it in the
// background.
func (r *rig) eventually(what string, ok func() bool) {
	r.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			r.t.Fatalf("не дождался: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (r *rig) state() proto.State {
	r.t.Helper()
	var st proto.State
	r.call(proto.MethodStateGet, nil, &st)
	return st
}

func (r *rig) longCall(method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return r.c.Call(ctx, method, params, result)
}

func codeOf(err error) int {
	var perr *proto.Error
	if errors.As(err, &perr) {
		return perr.Code
	}
	return 0
}

func serverIDs(st proto.State) string {
	var ids []string
	for _, s := range st.Servers {
		ids = append(ids, s.ID)
	}
	return strings.Join(ids, " ")
}

// The whole path of a new user: add a subscription, start, switch the
// server and the mode, edit the rules — and the engine is started once.
func TestSubscriptionSelectModeAndRulesWithoutRestart(t *testing.T) {
	sub := newSubServer(t, link("NL", "nl.example.com"), link("DE", "de.example.com"), "vmess://nope")
	r := startRig(t, emptyData(t), "ok")

	var added proto.SubsAddResult
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL + "/secret-key-1234567890", Name: "lider"}, &added); err != nil {
		t.Fatal(err)
	}
	if added.ID != "s1" || added.Servers != 2 {
		t.Fatalf("subs.add: %+v", added)
	}
	st := r.state()
	if serverIDs(st) != "lider-DE lider-NL" || st.Server.Selected == nil || *st.Server.Selected != "lider-DE" || st.Busy != nil {
		t.Fatalf("после добавления: серверы %q, выбран %s, busy %s", serverIDs(st), deref(st.Server.Selected), deref(st.Busy))
	}
	s := st.Subs[0]
	if s.Name != "lider" || s.Servers != 2 || s.UpdatedAt == nil || s.Error != nil || s.TotalBytes == nil || *s.TotalBytes != 100 || s.ExpireAt == nil {
		t.Errorf("подписка в состоянии: %+v", s)
	}
	// The key never leaves the daemon.
	if strings.Contains(s.URL, "secret-key") || !strings.HasSuffix(s.URL, "…7890") {
		t.Errorf("ссылка подписки не замаскирована: %q", s.URL)
	}
	if de := st.Servers[0]; de.Sub != "s1" || de.Protocol != "vless" || de.Via != proto.ViaSingBox || de.Host != "de.example.com" ||
		de.Country == nil || *de.Country != "DE" {
		t.Errorf("сервер в состоянии: %+v", de)
	}
	if !r.journalHas(proto.LevelInfo, "Пропущено 1") {
		r.waitState("запись о пропущенной ссылке", func(proto.State) bool { return r.journalHas(proto.LevelInfo, "Пропущено 1") })
	}
	if hwid := sub.lastHWID(); len(hwid) < 10 {
		t.Errorf("запрос подписки ушёл без x-hwid: %q", hwid)
	}

	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	pid := r.engine().PID
	if f := r.fake(); f.Now != "lider-DE" || f.Mode != "Rule" || strings.Join(f.Members, " ") != "lider-DE lider-NL" {
		t.Fatalf("движок после старта: %+v", f)
	}

	// Server: through the Clash API, the engine keeps running.
	r.call(proto.MethodServerSelect, proto.IDParams{ID: "lider-NL"}, nil)
	r.eventually("движок переключился на lider-NL", func() bool { return r.fake().Now == "lider-NL" })
	if st := r.state(); *st.Server.Selected != "lider-NL" {
		t.Errorf("выбранный сервер в состоянии: %s", *st.Server.Selected)
	}

	// Mode: the same, and the old connections are closed.
	r.call(proto.MethodModeSet, proto.ModeParams{Mode: proto.ModeGlobal}, nil)
	r.eventually("движок в режиме Global и закрыл соединения", func() bool { f := r.fake(); return f.Mode == "Global" && f.Closed == 1 })
	if st := r.state(); st.VPN.Mode != proto.ModeGlobal {
		t.Errorf("режим в состоянии: %s", st.VPN.Mode)
	}

	// Rules: the files change, the engine is left alone.
	var ref proto.RuleRef
	r.call(proto.MethodRulesAdd, proto.RulesAddParams{List: "vpn", Value: `C:\Tools\curl.exe`}, &ref)
	if ref.Kind != proto.KindProcess || ref.Value != "curl.exe" {
		t.Errorf("rules.add: %+v", ref)
	}
	r.call(proto.MethodRulesAdd, proto.RulesAddParams{List: "direct", Value: "https://www.Example.org/x"}, &ref)
	if ref.Kind != proto.KindSuffix || ref.Value != "example.org" {
		t.Errorf("rules.add сайта: %+v", ref)
	}
	raw, _ := os.ReadFile(rules.File(r.paths.RulesDir(), rules.TagVPN))
	if !strings.Contains(string(raw), `"curl.exe"`) {
		t.Errorf("curl.exe не попал в vpn.json")
	}
	var got proto.RulesGetResult
	r.call(proto.MethodRulesGet, nil, &got)
	if !contains(got.VPN.Processes, "curl.exe") || !contains(got.Direct.Suffixes, "example.org") || len(got.Auto) != 2 {
		t.Errorf("rules.get: vpn=%v direct=%v auto=%+v", got.VPN.Processes, got.Direct.Suffixes, got.Auto)
	}

	var where proto.RulesCheckResult
	r.call(proto.MethodRulesCheck, proto.RulesCheckParams{Target: "cdn.example.org"}, &where)
	if where.Route != "direct" || !strings.Contains(where.Because, "example.org") {
		t.Errorf("rules.check сайта из списка «напрямую»: %+v", where)
	}
	r.call(proto.MethodRulesCheck, proto.RulesCheckParams{Target: "whatever.example.net"}, &where)
	if where.Route != "vpn" || !strings.Contains(where.Because, "Весь трафик") {
		t.Errorf("rules.check в режиме «Весь трафик»: %+v", where)
	}

	r.call(proto.MethodRulesRemove, proto.RuleRef{List: "direct", Kind: ref.Kind, Value: ref.Value}, nil)
	r.call(proto.MethodRulesGet, nil, &got)
	if contains(got.Direct.Suffixes, "example.org") {
		t.Error("правило не удалилось")
	}

	// The mode and the server survive an engine restart: the config and the
	// sync after Ready both carry them.
	r.call(proto.MethodVPNRestart, nil, nil)
	r.waitPhase(proto.PhaseStarting)
	r.waitPhase(proto.PhaseOn)
	r.eventually("движок после перезапуска на прежнем сервере и режиме", func() bool {
		f := r.fake()
		return f.Now == "lider-NL" && f.Mode == "Global"
	})

	// Everything before the restart was one engine process.
	if n := enginetest.Starts(r.paths.Data); n != 2 {
		t.Errorf("движок запускался %d раз, жду 2: старт и vpn.restart", n)
	}
	if !engine.Alive(r.engine().PID) || r.engine().PID == pid {
		t.Errorf("после vpn.restart должен работать новый процесс")
	}
}

// A refresh that fails must not cost the user their servers; one that
// changes them restarts the engine, one that changes nothing does not.
func TestRefreshKeepsServersOnFailureAndRestartsOnChange(t *testing.T) {
	sub := newSubServer(t, link("DE", "de.example.com"), link("NL", "nl.example.com"))
	r := startRig(t, emptyData(t), "ok")
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "a"}, nil); err != nil {
		t.Fatal(err)
	}
	r.call(proto.MethodServerSelect, proto.IDParams{ID: "a-NL"}, nil)
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	pid := r.engine().PID

	// The provider is down.
	sub.set(http.StatusBadGateway)
	err := r.longCall(proto.MethodSubsUpdate, nil, nil)
	if codeOf(err) != proto.CodeFetchFailed {
		t.Fatalf("subs.update при недоступном провайдере: %v, жду код %d", err, proto.CodeFetchFailed)
	}
	st := r.state()
	if serverIDs(st) != "a-DE a-NL" || st.Subs[0].Error == nil || st.Subs[0].Servers != 2 || *st.Server.Selected != "a-NL" {
		t.Fatalf("после провала: серверы %q, ошибка %s, выбран %s", serverIDs(st), deref(st.Subs[0].Error), *st.Server.Selected)
	}
	if st.VPN.Phase != proto.PhaseOn || r.engine().PID != pid {
		t.Errorf("провал обновления тронул движок: phase=%s", st.VPN.Phase)
	}

	// Back, with the same list in another order: nothing to restart.
	sub.set(0, link("NL", "nl.example.com"), link("DE", "de.example.com"))
	if err := r.longCall(proto.MethodSubsUpdate, proto.SubsUpdateParams{ID: "s1"}, nil); err != nil {
		t.Fatal(err)
	}
	if st := r.state(); st.Subs[0].Error != nil || st.VPN.Phase != proto.PhaseOn || r.engine().PID != pid {
		t.Errorf("обновление без изменений: ошибка %s, phase %s", deref(st.Subs[0].Error), st.VPN.Phase)
	}

	// The selected server is gone and a new one came: the engine restarts
	// with the new set and the selection moves.
	sub.set(0, link("DE", "de.example.com"), link("FI", "fi.example.com"))
	if err := r.longCall(proto.MethodSubsUpdate, nil, nil); err != nil {
		t.Fatal(err)
	}
	r.waitState("VPN снова включён на новом наборе", func(st proto.State) bool {
		return st.VPN.Phase == proto.PhaseOn && serverIDs(st) == "a-DE a-FI"
	})
	if sel := *r.last.Server.Selected; sel != "a-DE" {
		t.Errorf("выбор после пропажи сервера: %s", sel)
	}
	if r.engine().PID == pid {
		t.Error("набор серверов изменился, а движок не перезапущен")
	}
	r.eventually("движок знает новый набор", func() bool {
		f := r.fake()
		return strings.Join(f.Members, " ") == "a-DE a-FI" && f.Now == "a-DE"
	})
	if !r.journalHas(proto.LevelProblem, "a-NL больше нет") {
		t.Errorf("в журнале нет записи о пропавшем сервере: %+v", r.journal)
	}

	// Removing the last subscription turns the VPN off: there is nothing
	// left to connect to.
	r.call(proto.MethodSubsRemove, proto.IDParams{ID: "s1"}, nil)
	r.waitPhase(proto.PhaseOff)
	if st := r.last; st.VPN.Desired != proto.DesiredOff || len(st.Servers) != 0 || len(st.Subs) != 0 || st.Server.Selected != nil {
		t.Errorf("после удаления последней подписки: %+v, серверов %d", st.VPN, len(st.Servers))
	}
}

func TestBadSubscriptionsLeaveNothingBehind(t *testing.T) {
	sub := newSubServer(t, link("DE", "de.example.com"))
	r := startRig(t, emptyData(t), "ok")

	sub.set(http.StatusNotFound)
	err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL}, nil)
	if codeOf(err) != proto.CodeFetchFailed || !strings.Contains(err.Error(), "404") {
		t.Fatalf("подписка с ошибкой 404: %v", err)
	}
	// Reachable, but with nothing usable in it.
	sub.set(0, "vmess://x", "vless://id@0.0.0.0:1#stub")
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL}, nil); codeOf(err) != proto.CodeFetchFailed {
		t.Fatalf("подписка без подходящих серверов: %v", err)
	}
	// Fine by us, refused by the engine.
	sub.set(0, link("DE", "de.example.com"))
	refusal := errors.New("unknown field")
	r.checkErr.Store(&refusal)
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL}, nil); codeOf(err) != proto.CodeInvalid {
		t.Fatalf("серверы, которые не принял движок: %v", err)
	}
	if err := r.longCall(proto.MethodSubsAddLink, proto.SubsAddLinkParams{Link: link("X", "x.example.com")}, nil); codeOf(err) != proto.CodeInvalid {
		t.Fatalf("ссылка, которую не принял движок: %v", err)
	}
	if st := r.state(); len(st.Subs) != 0 || len(st.Servers) != 0 || st.Busy != nil {
		t.Fatalf("после неудач что-то осталось: подписок %d, серверов %d, busy %s", len(st.Subs), len(st.Servers), deref(st.Busy))
	}

	// Once the engine agrees, the same subscription goes in — as s1's
	// successors: ids are not reused, but the list is clean.
	r.checkErr.Store(nil)
	var added proto.SubsAddResult
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "ok"}, &added); err != nil || added.Servers != 1 {
		t.Fatalf("годная подписка: %+v, %v", added, err)
	}
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL}, nil); codeOf(err) != proto.CodeInvalid {
		t.Errorf("та же ссылка второй раз: %v", err)
	}

	// A single server by link, and its removal.
	var one proto.IDParams
	if err := r.longCall(proto.MethodSubsAddLink, proto.SubsAddLinkParams{Link: "trojan://pw@home.example.com:443#Home"}, &one); err != nil || one.ID != "Home" {
		t.Fatalf("subs.addLink: %+v, %v", one, err)
	}
	if st := r.state(); serverIDs(st) != "ok-DE Home" || st.Servers[1].Sub != "" {
		t.Errorf("после ссылки: %q", serverIDs(st))
	}
	if err := r.tryCall(proto.MethodServerRemove, proto.IDParams{ID: "ok-DE"}, nil); codeOf(err) != proto.CodeInvalid {
		t.Errorf("удаление сервера подписки: %v", err)
	}
	r.call(proto.MethodServerRemove, proto.IDParams{ID: "Home"}, nil)

	// Favourites and their order.
	r.call(proto.MethodSubsRename, proto.SubsRenameParams{ID: added.ID, Name: "Основной"}, nil)
	r.call(proto.MethodServerFavorite, proto.ServerFavoriteParams{ID: "ok-DE", Favorite: true}, nil)
	st := r.state()
	if st.Subs[0].Name != "Основной" || !st.Servers[0].Favorite || st.Servers[0].ID != "ok-DE" {
		t.Errorf("переименование и избранное: %+v / %+v", st.Subs[0], st.Servers[0])
	}

	// All of it is on disk: a new daemon starts with the same profile.
	paths := r.paths
	r.shutdown()
	r = startRig(t, paths, "ok")
	if st := r.last; serverIDs(st) != "ok-DE" || st.Subs[0].Name != "Основной" || !st.Servers[0].Favorite {
		t.Errorf("после перезапуска службы: серверы %q, подписки %+v", serverIDs(st), st.Subs)
	}
}

func TestServerTestFillsPingsAndStats(t *testing.T) {
	sub := newSubServer(t, link("DE", "de.example.com"), link("dead", "gone.example.com"))
	r := startRig(t, emptyData(t), "ok")
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "a"}, nil); err != nil {
		t.Fatal(err)
	}
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)

	r.call(proto.MethodServerTest, nil, nil)
	r.waitState("результаты проверки", func(st proto.State) bool {
		return st.Busy == nil && len(st.Servers) == 2 && st.Servers[0].PingAt != nil && st.Servers[1].PingAt != nil
	})
	de, dead := r.last.Servers[0], r.last.Servers[1]
	if !de.Alive || de.Ping == nil || *de.Ping != enginetest.DelayOf("a-DE") || de.Stats == nil || de.Stats.Samples < 1 { // the health probe of the server in use counts too
		t.Errorf("живой сервер: %+v stats=%+v", de, de.Stats)
	}
	if dead.Alive || dead.Ping != nil || dead.Stats == nil || dead.Stats.Loss <= 0 {
		t.Errorf("молчащий сервер: %+v stats=%+v", dead, dead.Stats)
	}
	if sel := r.last.Server; sel.Ping == nil || *sel.Ping != *de.Ping {
		t.Errorf("пинг выбранного сервера: %+v", sel)
	}
	if !r.journalHas(proto.LevelInfo, "отвечают 1 из 2") {
		t.Errorf("в журнале нет итога проверки: %+v", r.journal)
	}
	if _, err := os.Stat(r.paths.StatsFile()); err != nil {
		t.Errorf("статистика не сохранена: %v", err)
	}

	// One server by id.
	r.call(proto.MethodServerTest, proto.ServerTestParams{IDs: []string{"a-DE"}}, nil)
	r.waitState("проверка одного сервера", func(st proto.State) bool {
		return st.Busy == nil && r.journalHas(proto.LevelInfo, "отвечают 1 из 1")
	})
}

// A server that needs Xray brings the helper up with the VPN and down with it.
func TestXrayHelperRunsForItsServers(t *testing.T) {
	paths := emptyData(t)
	// The parser sends servers to Xray only when the helper is installed.
	if err := os.WriteFile(paths.Engine(xrayName), []byte("stand-in"), 0o700); err != nil {
		t.Fatal(err)
	}
	reality := "vless://11111111-1111-4111-8111-111111111111@se.example.com:443?security=reality&sni=www.example.org&pbk=TWFkZVVw&sid=ab#[SE] made up"
	sub := newSubServer(t, link("DE", "de.example.com"), reality)
	r := startRig(t, paths, "ok")
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "a"}, nil); err != nil {
		t.Fatal(err)
	}
	st := r.state()
	if st.Servers[1].ID != "a-SE" || st.Servers[1].Via != proto.ViaXray || st.Servers[1].Host != "se.example.com" {
		t.Fatalf("сервер через Xray: %+v", st.Servers[1])
	}

	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	var es proto.EngineStatusResult
	r.call(proto.MethodEngineStatus, nil, &es)
	if len(es.Engines) != 2 || es.Engines[1].Name != xrayName || !es.Engines[1].Running {
		t.Fatalf("engine.status с помощником: %+v", es)
	}
	xpid := es.Engines[1].PID

	// sing-box reaches the server through the helper's local port, and the
	// helper has the real address.
	cfg, _ := os.ReadFile(paths.RunConfig())
	ob := testjson.Find(cfg, "outbounds", "tag", "a-SE")
	xcfg, _ := os.ReadFile(paths.XrayConfig())
	if testjson.Get(ob, "type") != "socks" || testjson.Get(ob, "server_port") != testjson.Get(xcfg, "inbounds.0.port") {
		t.Errorf("порт в config.json %s, в xray.json %s", testjson.Get(ob, "server_port"), testjson.Get(xcfg, "inbounds.0.port"))
	}
	if testjson.Get(xcfg, "outbounds.0.settings.vnext.0.address") != "se.example.com" {
		t.Errorf("xray.json: %s", testjson.Get(xcfg, "outbounds.0"))
	}
	if !strings.Contains(testjson.Get(cfg, "route.rules"), "process_path_regex") {
		t.Error("в конфиге нет правила для помощника")
	}
	hosts, _ := os.ReadFile(rules.File(paths.RulesDir(), rules.TagServersDNS))
	if !strings.Contains(string(hosts), "se.example.com") || !strings.Contains(string(hosts), "de.example.com") {
		t.Errorf("имена серверов не попали в servers-dns.json: %s", hosts)
	}

	// The helper dies: the VPN stays on, the journal says so, it comes back.
	killProcess(t, xpid)
	r.waitState("помощник снова работает", func(proto.State) bool {
		return r.journalHas(proto.LevelProblem, "Помощник Xray неожиданно остановился") &&
			r.journalHas(proto.LevelInfo, "Помощник Xray снова работает")
	})
	if r.last.VPN.Phase != proto.PhaseOn {
		t.Errorf("падение помощника уронило VPN: %s", r.last.VPN.Phase)
	}
	r.call(proto.MethodEngineStatus, nil, &es)
	if len(es.Engines) != 2 || !es.Engines[1].Running || es.Engines[1].PID == xpid {
		t.Errorf("помощник после падения: %+v", es.Engines)
	}
	newX := es.Engines[1].PID

	r.call(proto.MethodVPNStop, nil, nil)
	r.waitPhase(proto.PhaseOff)
	waitGone(t, newX)
}

func TestSettingsArePersistedAndRebuildTheConfig(t *testing.T) {
	paths := newData(t)
	r := startRig(t, paths, "ok")
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	pid := r.engine().PID

	// A setting the engine does not care about: no restart.
	r.call(proto.MethodSettingsSet, map[string]any{"patch": map[string]any{"notify": "all", "autoUpdateSubs": "off", "guard": false}}, nil)
	st := r.state()
	if st.Settings.Notify != "all" || st.Settings.AutoUpdateSubs != "off" || st.Settings.Guard || st.Guard.Enabled {
		t.Errorf("настройки в состоянии: %+v guard=%+v", st.Settings, st.Guard)
	}
	if r.engine().PID != pid {
		t.Error("настройка уведомлений перезапустила движок")
	}

	// One that is part of the config: rebuilt and restarted.
	r.call(proto.MethodSettingsSet, map[string]any{"patch": map[string]any{"localProxyPort": 2080, "ipv6": "block"}}, nil)
	r.waitPhase(proto.PhaseStarting)
	r.waitPhase(proto.PhaseOn)
	cfg, _ := os.ReadFile(paths.RunConfig())
	if testjson.Get(testjson.Find(cfg, "inbounds", "type", "mixed"), "listen_port") != "2080" ||
		testjson.Get(testjson.Find(cfg, "inbounds", "type", "tun"), "address") != `["172.19.0.1/30"]` {
		t.Errorf("конфиг после смены настроек: %s", testjson.Get(cfg, "inbounds"))
	}
	if r.engine().PID == pid {
		t.Error("смена локального прокси не перезапустила движок")
	}

	r.shutdown()
	r = startRig(t, paths, "ok")
	if s := r.last.Settings; s.LocalProxyPort != 2080 || s.IPv6 != "block" || s.Notify != "all" || s.Guard {
		t.Errorf("настройки после перезапуска службы: %+v", s)
	}
}

// "tunnel" puts IPv6 into the TUN only where the network has some: on a
// network without IPv6 an address on the TUN would make programs believe in
// IPv6 that leads nowhere. The engine is rebuilt when that changes, and only
// then.
func TestTunnelIPv6FollowsTheNetwork(t *testing.T) {
	paths := newData(t)
	r := startRig(t, paths, "ok")
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	tunAddress := func() string {
		cfg, _ := os.ReadFile(paths.RunConfig())
		return testjson.Get(testjson.Find(cfg, "inbounds", "type", "tun"), "address")
	}
	if got := tunAddress(); !strings.Contains(got, "fdfe:") {
		t.Fatalf("в сети есть IPv6, а у TUN его нет: %s", got)
	}
	pid := r.engine().PID

	// IPv6 is gone: the TUN loses its IPv6 address.
	r.routes(netmon.State{V4: true})
	r.waitState("перезапуск без IPv6", func(proto.State) bool { return r.journalHas(proto.LevelInfo, "в сети больше нет IPv6") })
	r.waitPhase(proto.PhaseOn)
	if got := tunAddress(); got != `["172.19.0.1/30"]` {
		t.Errorf("в сети нет IPv6, а TUN его даёт: %s", got)
	}
	if r.engine().PID == pid {
		t.Error("пропажа IPv6 не перезапустила движок")
	}
	pid = r.engine().PID

	// No network at all, then the same IPv4-only network back: nothing to
	// rebuild.
	r.routes(netmon.State{})
	r.routes(netmon.State{V4: true})
	r.routes(netmon.State{V4: true})
	time.Sleep(300 * time.Millisecond)
	if st := r.state(); r.engine().PID != pid || st.VPN.Phase != proto.PhaseOn {
		t.Errorf("сеть без перемен в IPv6 перезапустила движок: фаза %s", st.VPN.Phase)
	}

	// IPv6 is back: so is the address.
	r.routes(netmon.State{V4: true, V6: true})
	r.waitState("перезапуск с IPv6", func(proto.State) bool { return r.journalHas(proto.LevelInfo, "в сети появился IPv6") })
	r.waitPhase(proto.PhaseOn)
	if got := tunAddress(); !strings.Contains(got, "fdfe:") {
		t.Errorf("IPv6 вернулся, а у TUN его нет: %s", got)
	}

	// With "block" the network's IPv6 makes no difference to the config.
	r.call(proto.MethodSettingsSet, map[string]any{"patch": map[string]any{"ipv6": "block"}}, nil)
	r.waitPhase(proto.PhaseStarting)
	r.waitPhase(proto.PhaseOn)
	pid = r.engine().PID
	r.routes(netmon.State{V4: true})
	time.Sleep(300 * time.Millisecond)
	if r.engine().PID != pid {
		t.Error("при «блокировать» пропажа IPv6 перезапустила движок")
	}
}

// An old Strelka folder, as the tray left it. Only read, never written.
func TestLegacyImport(t *testing.T) {
	sub := newSubServer(t, link("DE", "de.example.com"), link("NL", "nl.example.com"))
	old := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(old, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("subscription.txt", "# вставь ссылку ниже\r\nlider "+sub.URL+"/a\r\nklex "+sub.URL+"/b\r\n")
	write("hwid.txt", "0ldstrelka0hwid1\n")
	write("rules/vpn.json", `{"version":3,"rules":[{"process_name":["MyGame.exe"]},{"domain_suffix":["blocked.example.com","*bad value*"]}]}`)
	write("rules/direct.json", `{"version":3,"rules":[{"ip_cidr":["203.0.113.0/24"]}]}`)
	write("server-stats.json", `{"servers":{"klex-NL":{"avg":40,"miss":0,"n":9,"seen":"2026-11-10T10:00:00Z"},"lider-DE":{"avg":300,"miss":0,"n":9,"seen":"2026-11-10T10:00:00Z"}}}`)
	// The old engine is not running: nobody listens on this port, so the
	// selection comes from the config. It says "auto", which no longer exists.
	write("config.json", `{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["auto","lider-DE"],"default":"auto"}],
		"experimental":{"clash_api":{"external_controller":"127.0.0.1:1","secret":"x"}}}`)
	before := snapshotDir(t, old)

	r := startRig(t, emptyData(t), "ok")
	var res proto.LegacyImportResult
	if err := r.longCall(proto.MethodLegacyImport, proto.LegacyImportParams{Dir: old}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Subs != 2 || res.Servers != 4 || res.Rules != 3 || len(res.Failed) != 0 || res.Mode != proto.ModeRule {
		t.Errorf("итог переноса: %+v", res)
	}
	st := r.state()
	// The ids are the old tray's tags: subscription name, a dash, the label.
	if serverIDs(st) != "lider-DE lider-NL klex-DE klex-NL" {
		t.Errorf("серверы после переноса: %q", serverIDs(st))
	}
	// "auto" has no counterpart; the best server by the old stats is taken.
	if st.Server.Selected == nil || *st.Server.Selected != "klex-NL" || res.Selected == nil || *res.Selected != "klex-NL" {
		t.Errorf("выбранный сервер: %s", deref(st.Server.Selected))
	}
	// The provider knows this machine by the old id.
	if sub.lastHWID() != "0ldstrelka0hwid1" {
		t.Errorf("подписка скачана с HWID %q, а не со старым", sub.lastHWID())
	}
	var got proto.RulesGetResult
	r.call(proto.MethodRulesGet, nil, &got)
	// The old lists come with what the built-in ones have gained since.
	if strings.Join(got.VPN.Processes, " ") != "MyGame.exe" || strings.Join(got.VPN.Suffixes, " ") != "blocked.example.com" ||
		strings.Join(got.Direct.IPs, " ") != "203.0.113.0/24" || strings.Join(got.Direct.Processes, " ") != "ExitLag.exe exitlag.exe" {
		t.Errorf("правила после переноса: vpn=%+v direct=%+v", got.VPN, got.Direct)
	}
	if after := snapshotDir(t, old); after != before {
		t.Errorf("папка старой Стрелки изменилась:\nбыло  %s\nстало %s", before, after)
	}

	// A second import adds nothing twice.
	if err := r.longCall(proto.MethodLegacyImport, proto.LegacyImportParams{Dir: old}, &res); err != nil {
		t.Fatal(err)
	}
	if st := r.state(); len(st.Subs) != 2 || len(st.Servers) != 4 {
		t.Errorf("повторный перенос: подписок %d, серверов %d", len(st.Subs), len(st.Servers))
	}
}

// snapshotDir lists every file of a folder with its size and time.
func snapshotDir(t *testing.T, dir string) string {
	t.Helper()
	var out []string
	filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			out = append(out, fmt.Sprintf("%s %d %d", filepath.Base(path), fi.Size(), fi.ModTime().UnixNano()))
		}
		return nil
	})
	return strings.Join(out, "; ")
}

func TestConnectionsAndTrafficTopics(t *testing.T) {
	r := startRig(t, newData(t), "ok")
	r.call(proto.MethodStateSubscribe, proto.TopicsParams{Topics: []string{proto.TopicConnections, proto.TopicTraffic}}, nil)
	r.call(proto.MethodVPNStart, nil, nil)

	var conns []proto.Connection
	gotTraffic := false
	deadline := time.After(10 * time.Second)
	for conns == nil || !gotTraffic || r.last.Conns.Total == 0 {
		select {
		case n, ok := <-r.c.Notifications():
			if !ok {
				t.Fatal("соединение закрылось")
			}
			switch n.Method {
			case proto.TopicConnections:
				if err := json.Unmarshal(n.Params, &conns); err != nil {
					t.Fatal(err)
				}
			case proto.TopicTraffic:
				var s proto.TrafficSample
				if err := json.Unmarshal(n.Params, &s); err != nil || s.T.IsZero() {
					t.Fatalf("traffic: %s (%v)", n.Params, err)
				}
				gotTraffic = true
			case proto.TopicState:
				json.Unmarshal(n.Params, &r.last)
			}
		case <-deadline:
			t.Fatalf("не дождался: соединения %v, трафик %v, conns в состоянии %+v", conns != nil, gotTraffic, r.last.Conns)
		}
	}
	if len(conns) != 2 {
		t.Fatalf("соединений %d, жду 2", len(conns))
	}
	via, direct := conns[0], conns[1]
	if via.Route != "vpn" || via.Process != "Browser.exe" || via.Host != "example.com" || via.Dest != "192.0.2.10:443" ||
		via.Chain[0] != "t-DE" || via.Down != 20 {
		t.Errorf("соединение через VPN: %+v", via)
	}
	if direct.Route != "direct" || direct.Process != "" || direct.Host != "192.0.2.20" {
		t.Errorf("соединение напрямую: %+v", direct)
	}
	if c, tr := r.last.Conns, r.last.Traffic; c.Total != 2 || c.VPN != 1 || tr.DownTotal != 2048 || tr.UpTotal != 1024 {
		t.Errorf("счётчики в состоянии: %+v %+v", c, tr)
	}
}

func TestCheckTarget(t *testing.T) {
	var set rules.Set
	set.Add(rules.TagVPN, proto.KindProcess, "Discord.exe")
	set.Add(rules.TagVPN, proto.KindSuffix, "discord.com")
	set.Add(rules.TagDirect, proto.KindProcess, "steam.exe")
	set.Add(rules.TagDirect, proto.KindSuffix, "steamcontent.com")
	set.Add(rules.TagDirect, proto.KindIP, "203.0.113.0/24")
	auto := func(tag, target string) bool { return tag == "itdog-inside" && target == "blocked.example.net" }

	cases := []struct {
		global        bool
		target, route string
		because       string
	}{
		{false, "Discord.exe", "vpn", "через VPN"},
		{false, "cdn.discord.com", "vpn", "discord.com"},
		{false, "steam.exe", "direct", "напрямую"},
		{false, "notepad.exe", "direct", "нет в списках"},
		{false, "blocked.example.net", "vpn", "автосписке"},
		{false, "plain.example.net", "direct", "Нет ни в одном списке"},
		{false, "192.168.1.1", "direct", "локальной сети"},
		{false, "203.0.113.7", "direct", "203.0.113.0/24"},
		// "All traffic" sends the rest to the VPN, but the direct list and
		// the local network still win.
		{true, "notepad.exe", "vpn", "Весь трафик"},
		{true, "plain.example.net", "vpn", "Весь трафик"},
		{true, "steam.exe", "direct", "напрямую"},
		{true, "a.steamcontent.com", "direct", "steamcontent.com"},
		{true, "10.0.0.5", "direct", "локальной сети"},
	}
	for _, c := range cases {
		kind, value, err := rules.Normalize("", c.target)
		if err != nil {
			t.Fatalf("%s: %v", c.target, err)
		}
		got := checkTarget(set, c.global, kind, value, auto)
		if got.Route != c.route || !strings.Contains(got.Because, c.because) {
			t.Errorf("%s (весь трафик: %v): %+v, жду %s и слова «%s»", c.target, c.global, got, c.route, c.because)
		}
	}
}
