package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"strelka/internal/clash"
	"strelka/internal/health"
	"strelka/internal/testjson"
	"strelka/proto"
)

// quickHealth is the default tuning a hundred times faster: the
// same proportions, so an outage that takes a quarter of a minute to confirm
// takes a fraction of a second here.
func quickHealth(o *Options) {
	ms := time.Millisecond
	t := health.Default()
	t.ProbeEvery, t.TrafficFresh, t.ProbeTimeout = 300*ms, 300*ms, 500*ms
	t.RetryAt, t.RetryEvery = []time.Duration{20 * ms, 50 * ms, 100 * ms}, 50*ms
	t.ConfirmSpan, t.DownProbeEvery, t.RecoverRetry = 120*ms, 100*ms, 20*ms
	t.SettleAfterNet, t.DirectFresh, t.OthersFresh = 150*ms, 150*ms, 600*ms
	t.HelperHold = 100 * ms
	t.CloseAfterOK, t.ReopenWithin, t.CalmFor, t.Grace = 100*ms, 3*time.Second, 3*time.Second, 200*ms
	o.Health, o.HealthTick = &t, 10*ms
}

// fakeDo calls one of the fake engine's test controls.
func (r *rig) fakeDo(path string) {
	r.t.Helper()
	cfg, err := os.ReadFile(r.paths.RunConfig())
	if err != nil {
		r.t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "http://"+testjson.Get(cfg, "experimental.clash_api.external_controller")+path, nil)
	req.Header.Set("Authorization", "Bearer "+testjson.Get(cfg, "experimental.clash_api.secret"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatalf("поддельный движок не отвечает: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		r.t.Fatalf("%s: %s", path, resp.Status)
	}
}

// answers sets what the fake engine says to the next probes of a server or
// of "direct"; the last answer repeats.
func (r *rig) answers(tag string, seq ...string) {
	r.t.Helper()
	r.fakeDo("/fake/script?tag=" + url.QueryEscape(tag) + "&answers=" + strings.Join(seq, ","))
}

func (r *rig) waitVerdict(v string) {
	r.t.Helper()
	r.waitState("health.verdict = "+v, func(st proto.State) bool { return st.Health.Verdict == v })
}

// settle keeps reading notifications for d, so that a test can say "and
// nothing more came".
func (r *rig) settle(d time.Duration) {
	r.t.Helper()
	for deadline := time.Now().Add(d); time.Now().Before(deadline); {
		r.drain()
		time.Sleep(10 * time.Millisecond)
	}
	// A call's answer comes after every notification queued before it.
	r.call(proto.MethodHello, proto.HelloParams{Client: "ctl", Version: "test", Proto: proto.Version}, nil)
	r.drain()
}

// drain takes the notifications already delivered, without waiting.
func (r *rig) drain() {
	r.t.Helper()
	for {
		select {
		case n, open := <-r.c.Notifications():
			if !open {
				r.t.Fatal("соединение со службой закрылось")
			}
			switch n.Method {
			case proto.TopicJournal:
				var e proto.JournalEntry
				if err := json.Unmarshal(n.Params, &e); err != nil {
					r.t.Fatalf("запись журнала не разобрана: %v", err)
				}
				r.journal = append(r.journal, e)
			case proto.TopicToast:
				var toast proto.Toast
				if err := json.Unmarshal(n.Params, &toast); err != nil {
					r.t.Fatalf("уведомление не разобрано: %v", err)
				}
				r.toasts = append(r.toasts, toast)
			case proto.TopicState:
				var st proto.State
				if err := json.Unmarshal(n.Params, &st); err != nil {
					r.t.Fatalf("State не разобран: %v", err)
				}
				r.last = st
			}
		default:
			return
		}
	}
}

func (r *rig) toastTitles() string {
	var out []string
	for _, t := range r.toasts {
		if t.Close {
			out = append(out, "(убрать)")
		} else {
			out = append(out, t.Title)
		}
	}
	return strings.Join(out, " | ")
}

func (r *rig) problems() []proto.JournalEntry {
	var out []proto.JournalEntry
	for _, e := range r.journal {
		if e.Level != proto.LevelInfo {
			out = append(out, e)
		}
	}
	return out
}

func startHealthy(t *testing.T) *rig {
	t.Helper()
	r := startRigWith(t, newData(t), "ok", quickHealth)
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	r.waitVerdict(proto.VerdictOK)
	return r
}

// The check of the stage, automated: a dead server is diagnosed with the
// internet and the other servers vouched for, reported once, and the
// incident ends with one "works again" when a live server is picked.
func TestDeadServerIsDiagnosedReportedAndEnds(t *testing.T) {
	r := startHealthy(t)
	if st := r.last; !st.Health.Internet || !st.Health.Network || st.Server.Ping == nil || len(st.Incidents) != 0 {
		t.Fatalf("здоровый VPN: health=%+v ping=%v incidents=%v", st.Health, st.Server.Ping, st.Incidents)
	}

	r.answers("t-DE", "fail")
	r.waitState("инцидент о сервере", func(st proto.State) bool {
		return st.Health.Verdict == proto.VerdictServerDown && len(st.Incidents) == 1
	})
	st := r.last
	if !st.Health.Internet || !st.Health.Network || st.Health.Since == nil || st.Server.Ping != nil {
		t.Errorf("health=%+v, пинг выбранного %v", st.Health, st.Server.Ping)
	}
	inc := st.Incidents[0]
	if inc.Kind != proto.IncidentServerDown || inc.Severity != proto.SeverityProblem || inc.Server == nil || *inc.Server != "t-DE" ||
		inc.Title != "Сервер Germany не отвечает" || inc.Body != "Интернет есть, другие серверы работают." {
		t.Errorf("инцидент: %+v", inc)
	}
	for _, s := range st.Servers {
		if alive := s.ID == "t-NL"; s.Alive != alive || s.PingAt == nil {
			t.Errorf("сервер %s: alive=%v, жду %v", s.ID, s.Alive, alive)
		}
	}
	r.settle(400 * time.Millisecond)
	if len(r.toasts) != 1 || r.toasts[0].ID != inc.ID || r.toasts[0].Title != inc.Title ||
		len(r.toasts[0].Actions) != 2 || r.toasts[0].Actions[0].ID != "switch:t-NL" {
		t.Fatalf("уведомления: %+v", r.toasts)
	}
	if p := r.problems(); len(p) != 1 || p[0].Incident == nil || *p[0].Incident != inc.ID || p[0].Cat != proto.CatServer {
		t.Fatalf("записи о проблеме: %+v", p)
	}

	// The button of the toast.
	r.call(proto.MethodIncidentAction, proto.IncidentActionParams{ID: inc.ID, Action: "switch:t-NL"}, nil)
	r.waitState("инцидент закрыт", func(st proto.State) bool {
		return st.Health.Verdict == proto.VerdictOK && len(st.Incidents) == 0
	})
	r.settle(300 * time.Millisecond)
	if st := r.last; st.Server.Selected == nil || *st.Server.Selected != "t-NL" || st.Server.Ping == nil {
		t.Errorf("после переключения: %+v", st.Server)
	}
	if len(r.toasts) != 2 || r.toasts[1].ID != inc.ID || r.toasts[1].Title != "Снова работает" ||
		!strings.HasPrefix(r.toasts[1].Body, "VPN снова работает — через сервер Netherlands. Не работало") ||
		r.toasts[1].Severity != proto.SeverityInfo {
		t.Errorf("уведомления после переключения: %s (%+v)", r.toastTitles(), r.toasts)
	}

	// The journal can be asked for the whole incident.
	var page proto.JournalListResult
	r.call(proto.MethodJournalList, proto.JournalListParams{Filter: &proto.JournalFilter{Incident: inc.ID}}, &page)
	if len(page.Entries) != 2 || page.More || page.Entries[0].Level != proto.LevelInfo || page.Entries[1].Level != proto.LevelProblem {
		t.Errorf("journal.list по инциденту: %+v", page)
	}
}

func TestNoInternetIsNotTheServersFault(t *testing.T) {
	r := startHealthy(t)
	r.answers("direct", "fail")
	r.answers("t-NL", "fail")
	r.answers("t-DE", "fail")
	r.waitVerdict(proto.VerdictNoInternet)
	r.settle(400 * time.Millisecond)
	if st := r.last; st.Health.Internet || !st.Health.Network || len(st.Incidents) != 0 || st.Health.Hint != nil {
		t.Errorf("health=%+v incidents=%+v", st.Health, st.Incidents)
	}
	if len(r.toasts) != 0 {
		t.Errorf("уведомления без интернета: %s", r.toastTitles())
	}
	if p := r.problems(); len(p) != 1 || !strings.HasPrefix(p[0].Text, "Нет интернета.") || p[0].Cat != proto.CatSystem {
		t.Errorf("записи: %+v", p)
	}

	// The internet returns, the server is still silent: now it is the server.
	r.answers("direct", "ok")
	r.answers("t-NL", "ok")
	r.waitState("виноват сервер", func(st proto.State) bool {
		return st.Health.Verdict == proto.VerdictServerDown && len(st.Incidents) == 1
	})
	r.settle(200 * time.Millisecond)
	if len(r.toasts) != 1 || r.toasts[0].Title != "Сервер Germany не отвечает" {
		t.Errorf("уведомления: %s", r.toastTitles())
	}
}

func TestAllServersDownOffersToRefreshSubscriptions(t *testing.T) {
	r := startHealthy(t)
	r.answers("t-NL", "fail")
	r.answers("t-DE", "fail")
	r.waitState("инцидент о серверах", func(st proto.State) bool {
		return st.Health.Verdict == proto.VerdictAllServersDown && len(st.Incidents) == 1
	})
	r.settle(200 * time.Millisecond)
	if inc := r.last.Incidents[0]; inc.Kind != proto.IncidentAllServersDown || inc.Server != nil || inc.Title != "Серверы не отвечают" {
		t.Errorf("инцидент: %+v", inc)
	}
	if len(r.toasts) != 1 || r.toasts[0].Actions[0].ID != proto.ActionUpdateSubs {
		t.Errorf("уведомления: %+v", r.toasts)
	}
	// There is nothing to refresh in this profile, and the daemon says so.
	err := r.tryCall(proto.MethodIncidentAction, proto.IncidentActionParams{ID: r.toasts[0].ID, Action: proto.ActionUpdateSubs}, nil)
	if codeOf(err) != proto.CodeNotFound {
		t.Errorf("update_subs без подписок: %v", err)
	}
}

func TestRestartAndSwitchingOffAreNotIncidents(t *testing.T) {
	r := startHealthy(t)
	r.call(proto.MethodVPNRestart, nil, nil)
	r.waitState("перезапуск", func(st proto.State) bool { return st.VPN.Phase == proto.PhaseStarting })
	r.waitPhase(proto.PhaseOn)
	r.waitVerdict(proto.VerdictOK)
	r.call(proto.MethodVPNStop, nil, nil)
	r.waitPhase(proto.PhaseOff)
	r.settle(400 * time.Millisecond)
	if len(r.toasts) != 0 || len(r.problems()) != 0 || len(r.last.Incidents) != 0 {
		t.Errorf("перезапуск и выключение: уведомления %q, записи %+v", r.toastTitles(), r.problems())
	}
	if r.last.Health.Verdict != proto.VerdictSettling {
		t.Errorf("у выключенного VPN диагноз %q", r.last.Health.Verdict)
	}
}

func TestTrafficOutweighsFailingProbes(t *testing.T) {
	// The poller reports traffic every two seconds whatever the tuning, so
	// here its word has to stay good for longer than the quick 300 ms.
	r := startRigWith(t, newData(t), "ok", func(o *Options) {
		quickHealth(o)
		o.Health.TrafficFresh = 10 * time.Second
	})
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	r.waitVerdict(proto.VerdictOK)
	r.fakeDo("/fake/traffic?on=1")
	// The poller reports traffic every two seconds; give it time to see some.
	time.Sleep(2500 * time.Millisecond)
	r.answers("t-DE", "fail")
	r.settle(1500 * time.Millisecond)
	if r.last.Health.Verdict != proto.VerdictOK || len(r.toasts) != 0 || len(r.problems()) != 0 {
		t.Errorf("трафик идёт, а диагноз %q, уведомления %q", r.last.Health.Verdict, r.toastTitles())
	}
}

func TestFailoverSwitchesOnceAndStays(t *testing.T) {
	r := startHealthy(t)
	r.call(proto.MethodSettingsSet, proto.SettingsSetParams{Patch: []byte(`{"failover":true}`)}, nil)
	r.call(proto.MethodServerFavorite, proto.ServerFavoriteParams{ID: "t-NL", Favorite: true}, nil)

	r.answers("t-DE", "fail")
	r.waitState("запасной сервер", func(st proto.State) bool {
		return st.Server.Selected != nil && *st.Server.Selected == "t-NL" && st.Health.Verdict == proto.VerdictOK
	})
	r.eventually("движок переключён", func() bool { return r.fake().Now == "t-NL" })
	r.waitState("инцидент закрыт", func(st proto.State) bool { return len(st.Incidents) == 0 })

	// The old server comes back. Nobody returns to it.
	r.answers("t-DE", "ok")
	r.settle(800 * time.Millisecond)
	if sel := r.last.Server.Selected; sel == nil || *sel != "t-NL" || r.fake().Now != "t-NL" {
		t.Errorf("после возвращения прежнего сервера выбран %v", sel)
	}
	if len(r.toasts) != 1 || r.toasts[0].Title != "Germany не отвечает, включён Netherlands" || r.toasts[0].Severity != proto.SeverityProblem {
		t.Errorf("уведомления: %s", r.toastTitles())
	}
	if p := r.problems(); len(p) != 1 || !strings.Contains(p[0].Text, "Запасной сервер включился сам") {
		t.Errorf("записи: %+v", p)
	}
	for _, e := range r.journal {
		if strings.HasPrefix(e.Text, "Выбран сервер") {
			t.Errorf("переключение службы записано как выбор пользователя: %q", e.Text)
		}
	}
}

func TestFailoverNeedsALiveFavourite(t *testing.T) {
	r := startHealthy(t)
	r.call(proto.MethodSettingsSet, proto.SettingsSetParams{Patch: []byte(`{"failover":true}`)}, nil)
	// t-NL is alive but is not a favourite: the user did not offer it.
	r.answers("t-DE", "fail")
	r.waitState("инцидент", func(st proto.State) bool {
		return st.Health.Verdict == proto.VerdictServerDown && len(st.Incidents) == 1
	})
	r.settle(300 * time.Millisecond)
	if sel := r.last.Server.Selected; sel == nil || *sel != "t-DE" {
		t.Errorf("служба переключилась на сервер не из избранных: %v", sel)
	}
	if len(r.toasts) != 1 || r.toasts[0].Title != "Сервер Germany не отвечает" {
		t.Errorf("уведомления: %s", r.toastTitles())
	}
}

func TestNetworkGoingAwayIsNoNetwork(t *testing.T) {
	r := startHealthy(t)
	r.answers("direct", "fail")
	r.answers("t-NL", "fail")
	r.answers("t-DE", "fail")
	r.network(false)
	r.waitVerdict(proto.VerdictNoNetwork)
	if st := r.last; st.Health.Network || st.Health.Internet {
		t.Errorf("health=%+v", st.Health)
	}

	r.answers("direct", "ok")
	r.answers("t-NL", "ok")
	r.answers("t-DE", "ok")
	r.network(true)
	r.waitVerdict(proto.VerdictOK)
	r.settle(300 * time.Millisecond)
	if len(r.toasts) != 0 || len(r.last.Incidents) != 0 {
		t.Errorf("пропажа сети уведомила: %s", r.toastTitles())
	}
	texts := ""
	for _, e := range r.journal {
		texts += e.Text + "\n"
	}
	if !strings.Contains(texts, "Нет сети.") || !strings.Contains(texts, "Сеть вернулась.") {
		t.Errorf("в журнале нет записей о сети:\n%s", texts)
	}
}

func TestEngineThatWillNotStartIsACriticalIncident(t *testing.T) {
	// Five crashes are the limit; the sixth start works.
	r := startRigWith(t, newData(t), "tun,tun,tun,tun,tun,ok", quickHealth)
	if err := r.tryCall(proto.MethodVPNStart, nil, nil); err != nil {
		t.Fatal(err)
	}
	r.waitState("инцидент", func(st proto.State) bool { return st.VPN.Phase == proto.PhaseFailed && len(st.Incidents) == 1 })
	st := r.last
	if st.VPN.FailReason == nil || *st.VPN.FailReason != proto.FailTUN || !strings.Contains(*st.VPN.FailText, "адаптер") {
		t.Errorf("причина: %s, %s", deref(st.VPN.FailReason), deref(st.VPN.FailText))
	}
	inc := st.Incidents[0]
	if st.Health.Verdict != proto.VerdictEngineFailed || inc.Kind != proto.IncidentEngineFailed ||
		inc.Severity != proto.SeverityCritical || inc.Body != *st.VPN.FailText {
		t.Errorf("verdict=%s, инцидент %+v", st.Health.Verdict, inc)
	}
	r.settle(200 * time.Millisecond)
	if len(r.toasts) != 1 || !r.toasts[0].Sticky || r.toasts[0].Actions[0].ID != proto.ActionRetry {
		t.Fatalf("уведомления: %+v", r.toasts)
	}
	if !r.journalHas(proto.LevelCritical, "VPN не запускается. Не создаётся сетевой адаптер") {
		t.Errorf("в журнале нет критичной записи: %+v", r.journal)
	}

	r.call(proto.MethodIncidentAction, proto.IncidentActionParams{ID: inc.ID, Action: proto.ActionRetry}, nil)
	r.waitState("VPN поднялся, инцидент закрыт", func(st proto.State) bool {
		return st.VPN.Phase == proto.PhaseOn && st.Health.Verdict == proto.VerdictOK && len(st.Incidents) == 0
	})
	r.settle(200 * time.Millisecond)
	if len(r.toasts) != 2 || r.toasts[1].Title != "Снова работает" || !strings.HasPrefix(r.toasts[1].Body, "VPN запустился.") {
		t.Errorf("уведомления после повтора: %s", r.toastTitles())
	}
}

// The daemon runs before anyone is logged in. A problem it found then is
// told to the first client that comes to listen; one that has passed is not.
func TestToastWaitsForSomeoneToShowIt(t *testing.T) {
	r := startRigWith(t, newData(t), "ok", quickHealth)
	r.call(proto.MethodStateUnsubscribe, proto.TopicsParams{Topics: []string{proto.TopicToast}}, nil)
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	r.waitVerdict(proto.VerdictOK)

	// One problem that comes and goes with nobody listening...
	r.answers("t-DE", "fail")
	r.waitState("инцидент", func(st proto.State) bool { return len(st.Incidents) == 1 })
	r.answers("t-DE", "ok")
	r.waitState("инцидент закрыт", func(st proto.State) bool { return len(st.Incidents) == 0 })
	// ...and one that is still there.
	r.answers("t-NL", "fail")
	r.answers("t-DE", "fail")
	r.waitState("второй инцидент", func(st proto.State) bool {
		return len(st.Incidents) == 1 && st.Incidents[0].Kind == proto.IncidentAllServersDown
	})
	r.settle(200 * time.Millisecond)
	if len(r.toasts) != 0 {
		t.Fatalf("уведомления пришли без подписки: %s", r.toastTitles())
	}

	r.call(proto.MethodStateSubscribe, proto.TopicsParams{Topics: []string{proto.TopicToast}}, nil)
	r.settle(200 * time.Millisecond)
	if len(r.toasts) != 1 || r.toasts[0].ID != r.last.Incidents[0].ID || r.toasts[0].Title != "Серверы не отвечают" {
		t.Fatalf("первому слушателю: %s, жду одно — о проблеме, которая ещё длится", r.toastTitles())
	}
	// Told once: subscribing again brings nothing.
	r.call(proto.MethodStateSubscribe, proto.TopicsParams{Topics: []string{proto.TopicToast}}, nil)
	r.settle(200 * time.Millisecond)
	if len(r.toasts) != 1 {
		t.Errorf("повторная подписка повторила уведомление: %s", r.toastTitles())
	}
}

func TestIncidentActionErrorsAndJournalPaging(t *testing.T) {
	r := startHealthy(t)
	act := func(action string) error {
		return r.tryCall(proto.MethodIncidentAction, proto.IncidentActionParams{ID: "i-20261116-1", Action: action}, nil)
	}
	for action, code := range map[string]int{
		"switch:нет-такого": proto.CodeNotFound,
		"сломать":           proto.CodeInvalidParams,
		proto.ActionRelease: proto.CodeUnsupported,
		proto.ActionOpen:    0,
		proto.ActionDismiss: 0,
	} {
		if got := codeOf(act(action)); got != code {
			t.Errorf("действие %q: код %d, жду %d", action, got, code)
		}
	}
	if err := r.tryCall(proto.MethodIncidentAction, nil, nil); codeOf(err) != proto.CodeInvalidParams {
		t.Errorf("incident.action без параметров: %v", err)
	}

	// retry on a working VPN restarts the engine.
	pid := r.engine().PID
	if err := act(proto.ActionRetry); err != nil {
		t.Fatal(err)
	}
	r.waitState("перезапуск", func(st proto.State) bool { return st.VPN.Phase == proto.PhaseStarting })
	r.waitPhase(proto.PhaseOn)
	if r.engine().PID == pid {
		t.Error("retry не перезапустил движок")
	}

	var all, page proto.JournalListResult
	r.call(proto.MethodJournalList, nil, &all)
	if len(all.Entries) < 3 || all.More || all.Entries[0].ID < all.Entries[1].ID {
		t.Fatalf("journal.list: %+v", all)
	}
	r.call(proto.MethodJournalList, proto.JournalListParams{Limit: 2}, &page)
	if len(page.Entries) != 2 || !page.More || page.Entries[0].ID != all.Entries[0].ID {
		t.Errorf("первая страница: %+v", page)
	}
	r.call(proto.MethodJournalList, proto.JournalListParams{Limit: 2, Before: page.Entries[1].ID}, &page)
	if len(page.Entries) == 0 || page.Entries[0].ID != all.Entries[2].ID {
		t.Errorf("вторая страница: %+v", page)
	}
	r.call(proto.MethodJournalList, proto.JournalListParams{Filter: &proto.JournalFilter{Levels: []string{proto.LevelCritical}}}, &page)
	if len(page.Entries) != 0 || page.Entries == nil {
		t.Errorf("критичных записей быть не должно: %+v", page)
	}
}

func TestSubscriptionAboutToEndIsAnIncident(t *testing.T) {
	sub := newSubServer(t, link("DE", "de.example.com"))
	soon := time.Now().Add(40 * time.Hour).Unix()
	sub.setInfo(fmt.Sprintf("upload=1; download=2; total=100; expire=%d", soon))
	r := startRigWith(t, emptyData(t), "ok", quickHealth)
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "Основная"}, nil); err != nil {
		t.Fatal(err)
	}
	r.waitState("предупреждение о подписке", func(st proto.State) bool { return len(st.Incidents) == 1 })
	inc := r.last.Incidents[0]
	if inc.Kind != proto.IncidentSubExpiring || inc.Title != "Подписка «Основная» скоро закончится" || !strings.HasPrefix(inc.Body, "Осталось 1 день.") {
		t.Errorf("инцидент: %+v", inc)
	}
	r.settle(100 * time.Millisecond)
	if len(r.toasts) != 1 || r.toasts[0].ID != inc.ID || r.toasts[0].Actions[0].ID != proto.ActionUpdateSubs {
		t.Fatalf("уведомления: %+v", r.toasts)
	}

	// The button refreshes; the provider says the plan was renewed.
	sub.setInfo("upload=1; download=2; total=100; expire=1893456000")
	r.call(proto.MethodIncidentAction, proto.IncidentActionParams{ID: inc.ID, Action: proto.ActionUpdateSubs}, nil)
	r.waitState("подписка продлена", func(st proto.State) bool { return len(st.Incidents) == 0 && st.Busy == nil })
	r.settle(100 * time.Millisecond)
	if len(r.toasts) != 2 || !r.toasts[1].Close || r.toasts[1].ID != inc.ID {
		t.Errorf("уведомления после продления: %+v", r.toasts)
	}
	if !r.journalHas(proto.LevelInfo, "Подписка «Основная» снова действует.") {
		t.Errorf("журнал: %+v", r.journal)
	}
}

// A new run of the daemon does not repeat a warning it has already given,
// and numbers its incidents on from the last one in the journal.
func TestWarningsAndIncidentNumbersSurviveARestart(t *testing.T) {
	sub := newSubServer(t, link("DE", "de.example.com"))
	sub.setInfo(fmt.Sprintf("upload=1; download=2; total=100; expire=%d", time.Now().Add(40*time.Hour).Unix()))
	paths := emptyData(t)
	r := startRigWith(t, paths, "ok", quickHealth)
	if err := r.longCall(proto.MethodSubsAdd, proto.SubsAddParams{URL: sub.URL, Name: "Основная"}, nil); err != nil {
		t.Fatal(err)
	}
	r.waitState("предупреждение", func(st proto.State) bool { return len(st.Incidents) == 1 })
	first := r.last.Incidents[0].ID
	r.shutdown()

	r = startRigWith(t, paths, "ok", quickHealth)
	r.settle(200 * time.Millisecond)
	if len(r.last.Incidents) != 1 || r.last.Incidents[0].Kind != proto.IncidentSubExpiring {
		t.Fatalf("после перезапуска карточки нет: %+v", r.last.Incidents)
	}
	if id := r.last.Incidents[0].ID; id == first {
		t.Errorf("новый запуск выдал тот же номер инцидента %s", id)
	}
	if len(r.toasts) != 0 || len(r.problems()) != 0 {
		t.Errorf("предупреждение повторено: %s, %+v", r.toastTitles(), r.problems())
	}
}

func TestReceivedCountsOnlyTheVPN(t *testing.T) {
	conn := func(id string, down int64, chain ...string) clash.Conn {
		return clash.Conn{ID: id, Download: down, Chains: chain}
	}
	snap := func(conns ...clash.Conn) clash.Snapshot { return clash.Snapshot{Conns: conns} }

	// The first look at an engine has nothing to compare with.
	prev, grew := received(nil, snap(conn("a", 500, "t-DE", "proxy")))
	if grew {
		t.Error("первый снимок принят за трафик")
	}
	if _, grew = received(prev, snap(conn("a", 500, "t-DE", "proxy"), conn("d", 9000, "direct"))); grew {
		t.Error("трафик мимо VPN принят за ответ сервера")
	}
	if _, grew = received(prev, snap(conn("a", 501, "t-DE", "proxy"))); !grew {
		t.Error("байты по соединению через VPN не замечены")
	}
	if _, grew = received(prev, snap(conn("b", 0, "t-DE", "proxy"))); grew {
		t.Error("новое соединение без единого байта в ответ принято за трафик")
	}
	if _, grew = received(prev, snap(conn("b", 10, "t-DE", "proxy"))); !grew {
		t.Error("новое соединение с ответом не замечено")
	}
}
