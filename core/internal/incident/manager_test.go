package incident

import (
	"strings"
	"testing"
	"time"

	"strelka/internal/health"
	"strelka/internal/health/healthtest"
	"strelka/proto"
)

var names = map[string]string{"a-DE": "Германия", "b-NL": "Нидерланды", "c-FI": "Финляндия"}

// stand is the health automaton in its test world with a Manager hung on
// its diagnoses, the way the daemon wires the two together.
type stand struct {
	*healthtest.World
	m       *Manager
	level   string
	off     bool
	journal []Entry
	toasts  []proto.Toast
}

func newStand(t *testing.T, level string) *stand {
	s := &stand{World: healthtest.NewWorld(t), m: New(health.Default()), level: level}
	s.OnStep = func(now time.Time, d health.Diagnosis) {
		in := Input{Diag: d, Off: s.off, Level: s.level, Server: Named{ID: d.Server, Name: names[d.Server]}}
		if len(d.Alive) > 0 {
			in.Switch = &Named{ID: d.Alive[0], Name: names[d.Alive[0]]}
		}
		s.take(s.m.Observe(now, in))
	}
	s.Send(health.Phase{Phase: proto.PhaseOn})
	s.Run(2 * time.Second)
	s.Want(proto.VerdictOK)
	return s
}

func (s *stand) take(out Output) {
	s.journal = append(s.journal, out.Journal...)
	s.toasts = append(s.toasts, out.Toasts...)
}

// act is the user doing something: the grace window opens.
func (s *stand) act() { s.m.UserAction(s.Now) }

// down kills the server and waits for the verdict.
func (s *stand) down() {
	s.T.Helper()
	s.ServerUp = false
	s.Until(proto.VerdictServerDown, 2*time.Minute)
}

func (s *stand) up() {
	s.T.Helper()
	s.ServerUp = true
	s.Until(proto.VerdictOK, 2*time.Minute)
}

func (s *stand) titles() string {
	var out []string
	for _, t := range s.toasts {
		switch {
		case t.Close:
			out = append(out, "(убрать)")
		default:
			out = append(out, t.Title)
		}
	}
	return strings.Join(out, " | ")
}

func (s *stand) incidentIDs() map[string]bool {
	ids := map[string]bool{}
	for _, e := range s.journal {
		if e.Incident != "" {
			ids[e.Incident] = true
		}
	}
	return ids
}

func (s *stand) lines() string {
	var out []string
	for _, e := range s.journal {
		out = append(out, "["+e.Level+"] "+e.Text)
	}
	return strings.Join(out, "\n")
}

// ---- the scenarios of the health rules ------------------------------------

func TestOneMissIsNothing(t *testing.T) {
	s := newStand(t, proto.NotifyAll)
	s.Run(28 * time.Second)
	s.ServerUp = false
	s.Run(8 * time.Second) // one probe lost, the next gets through
	s.ServerUp = true
	s.Run(10 * time.Minute)
	if len(s.journal)+len(s.toasts) != 0 {
		t.Errorf("один промах дал записи %q и уведомления %q", s.lines(), s.titles())
	}
}

func TestServerDownIsOneIncidentOneToast(t *testing.T) {
	s := newStand(t, proto.NotifyImportant)
	s.Run(28 * time.Second)
	s.down()
	s.Run(5 * time.Minute)

	if len(s.toasts) != 1 || len(s.journal) != 1 {
		t.Fatalf("уведомлений %d (%s), записей %d:\n%s", len(s.toasts), s.titles(), len(s.journal), s.lines())
	}
	toast, line := s.toasts[0], s.journal[0]
	if toast.Title != "Сервер Германия не отвечает" || toast.Body != "Интернет есть, другие серверы работают." ||
		toast.Severity != proto.SeverityProblem || toast.Sticky || toast.Close {
		t.Errorf("уведомление: %+v", toast)
	}
	if len(toast.Actions) != 2 || toast.Actions[0].ID != "switch:b-NL" || toast.Actions[0].Label != "Переключить на Нидерланды" ||
		toast.Actions[1].ID != proto.ActionOpen {
		t.Errorf("кнопки: %+v", toast.Actions)
	}
	if line.Cat != proto.CatServer || line.Level != proto.LevelProblem || line.Incident != toast.ID ||
		line.Text != "Сервер Германия не отвечает. Интернет есть, другие серверы работают." {
		t.Errorf("запись журнала: %+v", line)
	}
	list := s.m.Incidents()
	if len(list) != 1 || list[0].ID != toast.ID || list[0].Kind != proto.IncidentServerDown ||
		list[0].Server == nil || *list[0].Server != "a-DE" || list[0].Flaps != 0 || list[0].Unstable {
		t.Errorf("State.incidents: %+v", list)
	}

	// It comes back: the incident ends after ten calm seconds, with one
	// "works again" that says how long it was.
	s.ServerUp = true
	s.Until(proto.VerdictOK, time.Minute)
	s.Run(9 * time.Second)
	if len(s.toasts) != 1 || len(s.m.Incidents()) != 1 {
		t.Fatalf("инцидент закрыт раньше, чем ok продержался 10 с: %s", s.titles())
	}
	s.Run(2 * time.Second)
	if len(s.toasts) != 2 || len(s.m.Incidents()) != 0 {
		t.Fatalf("после 10 с ok: уведомления %q, открытых инцидентов %d", s.titles(), len(s.m.Incidents()))
	}
	back := s.toasts[1]
	if back.ID != toast.ID || back.Title != "Снова работает" || back.Severity != proto.SeverityInfo ||
		!strings.Contains(back.Body, "Сервер Германия снова отвечает. Не работало 5 мин") || len(back.Actions) != 0 {
		t.Errorf("окончание: %+v", back)
	}
	if last := s.journal[len(s.journal)-1]; last.Level != proto.LevelInfo || last.Incident != toast.ID || last.Text != back.Body {
		t.Errorf("запись об окончании: %+v", last)
	}
}

func TestNoInternetIsJournalOnly(t *testing.T) {
	s := newStand(t, proto.NotifyAll)
	s.Run(28 * time.Second)
	s.Internet = false
	s.Until(proto.VerdictNoInternet, time.Minute)
	s.Run(3 * time.Minute)
	if len(s.m.Incidents()) != 0 {
		t.Errorf("у «нет интернета» появилась карточка: %+v", s.m.Incidents())
	}
	s.Internet = true
	s.Until(proto.VerdictOK, time.Minute)
	s.Run(15 * time.Second)

	if len(s.toasts) != 0 {
		t.Errorf("уведомления при пропавшем интернете: %s", s.titles())
	}
	if len(s.journal) != 2 || !strings.HasPrefix(s.journal[0].Text, "Нет интернета.") ||
		!strings.HasPrefix(s.journal[1].Text, "Интернет вернулся. Его не было 3 мин") ||
		s.journal[0].Cat != proto.CatSystem || s.journal[0].Incident == "" || s.journal[0].Incident != s.journal[1].Incident {
		t.Errorf("журнал:\n%s", s.lines())
	}
}

func TestSleepForAnHourIsNoIncident(t *testing.T) {
	s := newStand(t, proto.NotifyAll)
	s.Run(28 * time.Second)
	s.ServerUp = false
	s.Run(9 * time.Second)
	s.Sleep(time.Hour)
	s.Run(14 * time.Second) // the network is still coming up
	s.ServerUp = true
	s.Run(5 * time.Minute)
	if len(s.journal)+len(s.toasts) != 0 {
		t.Errorf("после сна: записи %q, уведомления %q", s.lines(), s.titles())
	}
}

func TestFiveFallsInTenMinutesAreOneUnstableIncident(t *testing.T) {
	s := newStand(t, proto.NotifyImportant)
	s.MissTakes = 100 * time.Millisecond
	s.Run(20 * time.Second)
	start := s.Now
	for i := 0; i < 5; i++ {
		s.down()
		s.Run(30 * time.Second)
		s.up()
		s.Run(30 * time.Second)
	}
	if took := s.Now.Sub(start); took > 10*time.Minute {
		t.Fatalf("пять падений заняли %v, сценарий — про десять минут", took)
	}

	if ids := s.incidentIDs(); len(ids) != 1 {
		t.Errorf("инцидентов %d, жду один:\n%s", len(ids), s.lines())
	}
	list := s.m.Incidents()
	if len(list) != 1 || !list[0].Unstable || list[0].Flaps != 4 || list[0].Title != "Соединение нестабильно" ||
		list[0].Body != "Сервер Германия то отвечает, то нет." {
		t.Fatalf("после пяти падений ok держится 30 с, инцидент должен быть открыт и нестабилен: %+v", list)
	}
	// One notification of the problem, and the end of its first round.
	// Nothing for the four that followed.
	problems := 0
	for _, toast := range s.toasts {
		if toast.ID != list[0].ID {
			t.Errorf("уведомление с чужим id: %+v", toast)
		}
		if toast.Severity == proto.SeverityProblem {
			problems++
		}
	}
	if problems != 1 || len(s.toasts) != 2 || s.toasts[1].Title != "Снова работает" {
		t.Errorf("уведомления: %s; жду одно о проблеме и одно об окончании первого падения", s.titles())
	}

	// It stays open until five calm minutes have passed, and closes quietly.
	s.Run(4 * time.Minute)
	if len(s.m.Incidents()) != 1 {
		t.Error("нестабильный инцидент закрылся раньше пяти спокойных минут")
	}
	s.Run(time.Minute)
	if len(s.m.Incidents()) != 0 {
		t.Error("прошло пять спокойных минут, а инцидент открыт")
	}
	if len(s.toasts) != 2 {
		t.Errorf("закрытие нестабильного инцидента уведомило: %s", s.titles())
	}
	if last := s.journal[len(s.journal)-1].Text; !strings.HasPrefix(last, "Соединение снова стабильно.") {
		t.Errorf("последняя запись: %q", last)
	}
}

func TestUserRestartIsNoIncident(t *testing.T) {
	s := newStand(t, proto.NotifyAll)
	s.Run(time.Minute)
	s.act()
	s.Send(health.Phase{Phase: proto.PhaseStarting})
	s.Run(3 * time.Second)
	s.Send(health.Phase{Phase: proto.PhaseOn})
	s.Run(5 * time.Minute)
	if len(s.journal)+len(s.toasts) != 0 {
		t.Errorf("перезапуск по кнопке: записи %q, уведомления %q", s.lines(), s.titles())
	}
}

func TestEngineCrashAndRecoveryIsNoIncident(t *testing.T) {
	s := newStand(t, proto.NotifyAll)
	s.Run(time.Minute)
	s.Send(health.Phase{Phase: proto.PhaseRecovering})
	s.Run(4 * time.Second)
	s.Send(health.Phase{Phase: proto.PhaseOn})
	s.Run(5 * time.Minute)
	if len(s.journal)+len(s.toasts) != 0 {
		t.Errorf("движок упал и поднялся сам: записи %q, уведомления %q", s.lines(), s.titles())
	}
}

func TestTrafficWithAMissedProbeIsNoIncident(t *testing.T) {
	s := newStand(t, proto.NotifyAll)
	s.Run(28 * time.Second)
	s.ServerUp = false
	s.Run(4 * time.Second)
	s.Traffic(3 * time.Minute)
	if len(s.journal)+len(s.toasts) != 0 {
		t.Errorf("трафик шёл, зонд не прошёл: записи %q, уведомления %q", s.lines(), s.titles())
	}
}

// ---- the rules of the stage ------------------------------------------------

// The same story at the three levels: the VPN connects, the server dies,
// the server comes back.
func TestLevelsShowDifferentThings(t *testing.T) {
	want := map[string]string{
		proto.NotifyAll:       "VPN подключён | Сервер Германия не отвечает | Снова работает",
		proto.NotifyImportant: "Сервер Германия не отвечает | Снова работает",
		proto.NotifySilent:    "",
	}
	for level, titles := range want {
		s := newStand(t, level)
		s.toasts = append(s.toasts, s.m.Notice(level, Connected(Named{ID: "a-DE", Name: "Германия"}, 80))...)
		s.Run(28 * time.Second)
		s.down()
		s.Run(time.Minute)
		s.up()
		s.Run(15 * time.Second)
		if got := s.titles(); got != titles {
			t.Errorf("уровень %q: уведомления %q, жду %q", level, got, titles)
		}
		// The journal gets everything at every level.
		if len(s.journal) != 2 {
			t.Errorf("уровень %q: записей в журнале %d, жду две:\n%s", level, len(s.journal), s.lines())
		}
	}
}

func TestEndIsNotToldIfTheBeginningWasNot(t *testing.T) {
	s := newStand(t, proto.NotifySilent)
	s.Run(28 * time.Second)
	s.down()
	// Notifications come back on while the problem is still there, then it
	// ends: there is nothing to report the end of.
	s.level = proto.NotifyImportant
	s.Run(time.Minute)
	s.up()
	s.Run(15 * time.Second)
	if len(s.toasts) != 0 {
		t.Errorf("о проблеме молчали, а пришло: %s", s.titles())
	}

	// The other way round: told at the start, silenced before the end.
	s = newStand(t, proto.NotifyImportant)
	s.Run(28 * time.Second)
	s.down()
	s.level = proto.NotifySilent
	s.up()
	s.Run(15 * time.Second)
	if s.titles() != "Сервер Германия не отвечает" {
		t.Errorf("уровень «тихо» к концу проблемы: %s", s.titles())
	}
}

func TestUpdateReplacesTheToast(t *testing.T) {
	s := newStand(t, proto.NotifyImportant)
	s.Run(28 * time.Second)
	s.down()
	// A hint turns up in the engine's log.
	s.Send(health.LogHint{Server: "a-DE", Hint: health.HintKeys})
	// Then the other servers go quiet as well.
	s.Others = nil
	s.Until(proto.VerdictAllServersDown, 2*time.Minute)

	if len(s.toasts) != 3 {
		t.Fatalf("уведомлений %d (%s), жду открытие и два уточнения", len(s.toasts), s.titles())
	}
	for _, toast := range s.toasts {
		if toast.ID != s.toasts[0].ID || toast.Close {
			t.Errorf("уточнение пришло не заменой того же уведомления: %+v", toast)
		}
	}
	if !strings.Contains(s.toasts[1].Body, "Похоже, сервер отклонил подключение") || s.toasts[1].Title != s.toasts[0].Title {
		t.Errorf("уточнение с подсказкой: %+v", s.toasts[1])
	}
	if s.toasts[2].Title != "Серверы не отвечают" || s.toasts[2].Actions[0].ID != proto.ActionUpdateSubs {
		t.Errorf("смена причины: %+v", s.toasts[2])
	}
	if list := s.m.Incidents(); len(list) != 1 || list[0].Kind != proto.IncidentAllServersDown || list[0].Server != nil {
		t.Errorf("State.incidents: %+v", list)
	}
	if ids := s.incidentIDs(); len(ids) != 1 || len(s.journal) != 3 {
		t.Errorf("журнал должен вести один инцидент тремя записями:\n%s", s.lines())
	}

	// Nothing changes, nothing is sent again.
	s.Run(10 * time.Minute)
	if len(s.toasts) != 3 {
		t.Errorf("без изменений пришло ещё: %s", s.titles())
	}
}

func TestSameTroubleSoonAfterIsTheSameIncident(t *testing.T) {
	s := newStand(t, proto.NotifyImportant)
	s.Run(28 * time.Second)
	s.down()
	s.up()
	s.Run(2 * time.Minute)
	if len(s.m.Incidents()) != 0 {
		t.Fatal("инцидент не закрылся")
	}
	first := s.toasts[0].ID

	s.down()
	list := s.m.Incidents()
	if len(list) != 1 || list[0].ID != first || list[0].Flaps != 1 || list[0].Unstable {
		t.Fatalf("повтор через две минуты: %+v", list)
	}
	if got := s.titles(); got != "Сервер Германия не отвечает | Снова работает" {
		t.Errorf("повтор уведомил: %s", got)
	}
	if last := s.journal[len(s.journal)-1]; !strings.Contains(last.Text, "опять") || last.Incident != first {
		t.Errorf("запись о повторе: %+v", last)
	}
	s.up()
	s.Run(15 * time.Second)
	if got := s.titles(); got != "Сервер Германия не отвечает | Снова работает" {
		t.Errorf("конец повтора, о котором не уведомляли: %s", got)
	}

	// Six minutes of peace later it is a new incident, with its own toast.
	s.Run(6 * time.Minute)
	s.down()
	if list := s.m.Incidents(); len(list) != 1 || list[0].ID == first || list[0].Flaps != 0 {
		t.Errorf("та же проблема через шесть минут: %+v (первый был %s)", list, first)
	}
	if len(s.toasts) != 3 || s.toasts[2].Severity != proto.SeverityProblem {
		t.Errorf("новый инцидент: %s", s.titles())
	}
}

func TestADifferentTroubleIsADifferentIncident(t *testing.T) {
	s := newStand(t, proto.NotifyImportant)
	s.Run(28 * time.Second)
	s.Internet = false
	s.Until(proto.VerdictNoInternet, time.Minute)
	s.Internet = true
	s.Until(proto.VerdictOK, time.Minute)
	s.Run(time.Minute)
	s.down()
	if ids := s.incidentIDs(); len(ids) != 2 {
		t.Errorf("сначала не было интернета, потом умер сервер — это два инцидента, а их %d:\n%s", len(ids), s.lines())
	}
	if s.titles() != "Сервер Германия не отвечает" {
		t.Errorf("уведомления: %s", s.titles())
	}
}

// A dead server the user picked is diagnosed in 15-20 s, inside the grace
// window. The incident waits for the window to close and opens then.
func TestProblemThatOutlivesTheGraceWindowIsReported(t *testing.T) {
	s := newStand(t, proto.NotifyImportant)
	s.Run(time.Minute)
	s.ServerUp = false
	s.act()
	picked := s.Now
	s.Send(health.Select{Server: "b-NL"})
	took := s.Until(proto.VerdictServerDown, time.Minute)
	if took >= 20*time.Second {
		t.Fatalf("диагноз через %v — сценарий про диагноз внутри льготного окна", took)
	}
	if len(s.toasts)+len(s.journal) != 0 {
		t.Errorf("инцидент открыт через %v после действия пользователя, раньше 20 с", s.Now.Sub(picked))
	}
	s.Run(20*time.Second - s.Now.Sub(picked) + time.Second)
	if len(s.toasts) != 1 || s.toasts[0].Title != "Сервер Нидерланды не отвечает" || len(s.journal) != 1 {
		t.Errorf("после окна: уведомления %q, журнал:\n%s", s.titles(), s.lines())
	}

	// Picking a live one ends it: one "works again" with the duration.
	s.ServerUp = true
	s.act()
	s.Send(health.Select{Server: "c-FI"})
	s.Run(15 * time.Second)
	if len(s.toasts) != 2 || s.toasts[1].Title != "Снова работает" || len(s.m.Incidents()) != 0 {
		t.Fatalf("после выбора живого сервера: %s", s.titles())
	}
	// The dead server did not come back; the user went elsewhere.
	if body := s.toasts[1].Body; !strings.HasPrefix(body, "VPN снова работает — через сервер Финляндия. Не работало") {
		t.Errorf("окончание после переключения: %q", body)
	}
}

func TestSwitchingOffEndsTheIncidentWithoutFanfare(t *testing.T) {
	s := newStand(t, proto.NotifyImportant)
	s.Run(28 * time.Second)
	s.down()
	s.act()
	s.off = true
	s.Send(health.Phase{Phase: proto.PhaseStopping})
	s.Send(health.Phase{Phase: proto.PhaseOff})

	if len(s.m.Incidents()) != 0 {
		t.Error("VPN выключен, а инцидент открыт")
	}
	if len(s.toasts) != 2 || !s.toasts[1].Close || s.toasts[1].ID != s.toasts[0].ID {
		t.Errorf("уведомление должно быть убрано, а не заменено на «снова работает»: %s", s.titles())
	}
	if last := s.journal[len(s.journal)-1]; last.Level != proto.LevelInfo || !strings.Contains(last.Text, "VPN выключен") {
		t.Errorf("запись: %+v", last)
	}

	// Back on with the server still dead: a new incident, not a reopening.
	s.off = false
	s.act()
	s.Send(health.Phase{Phase: proto.PhaseOn})
	s.Until(proto.VerdictServerDown, time.Minute)
	s.Run(10 * time.Second)
	if ids := s.incidentIDs(); len(ids) != 2 {
		t.Errorf("после включения заново: инцидентов %d:\n%s", len(ids), s.lines())
	}
}

func TestEngineFailedIsCriticalAndDoesNotWait(t *testing.T) {
	s := newStand(t, proto.NotifyImportant)
	s.act() // the user pressed "start"; the failure is the answer, not a side effect
	fail := health.Diagnosis{Verdict: proto.VerdictEngineFailed, Reason: proto.FailTUN, Network: true, Internet: true}
	in := Input{Diag: fail, Level: proto.NotifyImportant, FailText: "Не создаётся сетевой адаптер."}
	s.take(s.m.Observe(s.Now, in))

	if len(s.toasts) != 1 {
		t.Fatalf("уведомлений %d", len(s.toasts))
	}
	toast := s.toasts[0]
	if toast.Severity != proto.SeverityCritical || !toast.Sticky || toast.Title != "VPN не запускается" ||
		toast.Body != "Не создаётся сетевой адаптер." || toast.Actions[0].ID != proto.ActionRetry {
		t.Errorf("уведомление: %+v", toast)
	}
	if e := s.journal[0]; e.Level != proto.LevelCritical || e.Cat != proto.CatVPN {
		t.Errorf("запись: %+v", e)
	}
	if list := s.m.Incidents(); len(list) != 1 || list[0].Severity != proto.SeverityCritical || list[0].Server != nil {
		t.Errorf("State.incidents: %+v", list)
	}
}

func TestFailoverIsToldOnceAndEndsQuietly(t *testing.T) {
	s := newStand(t, proto.NotifyImportant)
	s.Run(28 * time.Second)
	// The daemon switches the moment the verdict comes and says so here.
	s.OnStep = nil
	s.down()
	d := s.M.Diagnosis()
	s.take(s.m.Observe(s.Now, Input{
		Diag: d, Level: s.level, Server: Named{ID: "a-DE", Name: "Германия"},
		Moved: &Named{ID: "b-NL", Name: "Нидерланды"},
	}))
	if len(s.toasts) != 1 || s.toasts[0].Title != "Германия не отвечает, включён Нидерланды" ||
		s.toasts[0].Severity != proto.SeverityProblem || len(s.toasts[0].Actions) != 1 {
		t.Fatalf("уведомление о запасном сервере: %+v", s.toasts)
	}
	for i := 0; i < 15; i++ {
		ok := health.Diagnosis{Verdict: proto.VerdictOK, Server: "b-NL", Network: true, Internet: true}
		s.take(s.m.Observe(s.Now.Add(time.Duration(i)*time.Second), Input{Diag: ok, Level: s.level, Server: Named{ID: "b-NL"}}))
	}
	if len(s.m.Incidents()) != 0 || len(s.toasts) != 1 {
		t.Errorf("после переключения: открыто %d, уведомления %s", len(s.m.Incidents()), s.titles())
	}
	if len(s.journal) != 2 {
		t.Errorf("журнал:\n%s", s.lines())
	}
}

// ---- subscriptions ---------------------------------------------------------

func TestSubscriptionWarnings(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	num := func(n int64) *int64 { return &n }
	m := New(health.Default())

	list := []SubStatus{
		{ID: "s1", Name: "Основная", ExpireAt: at(50 * time.Hour)},
		{ID: "s2", Name: "Запасная", ExpireAt: at(30 * 24 * time.Hour), Used: num(10 << 30), Total: num(100 << 30)},
		{ID: "s3", Name: "Без срока"},
	}
	out := m.Subs(now, proto.NotifyImportant, list)
	if len(out.Toasts) != 1 || len(out.Journal) != 1 || !out.Changed {
		t.Fatalf("за 50 часов до срока: %+v", out)
	}
	toast := out.Toasts[0]
	if toast.Title != "Подписка «Основная» скоро закончится" || !strings.HasPrefix(toast.Body, "Осталось 2 дня.") ||
		toast.Severity != proto.SeverityProblem || toast.Actions[0].ID != proto.ActionUpdateSubs {
		t.Errorf("уведомление: %+v", toast)
	}
	if inc := m.Incidents(); len(inc) != 1 || inc[0].Kind != proto.IncidentSubExpiring || inc[0].ID != toast.ID {
		t.Errorf("State.incidents: %+v", inc)
	}

	// Looking again says nothing new; a day later only the text moves on.
	if out := m.Subs(now.Add(time.Hour), proto.NotifyImportant, list); len(out.Toasts)+len(out.Journal) != 0 || out.Changed {
		t.Errorf("повторная проверка: %+v", out)
	}
	out = m.Subs(now.Add(30*time.Hour), proto.NotifyImportant, list)
	if len(out.Toasts)+len(out.Journal) != 0 || !out.Changed || !strings.HasPrefix(m.Incidents()[0].Body, "Осталось меньше суток.") {
		t.Errorf("через 30 часов: %+v, %+v", out, m.Incidents())
	}

	// A restart of the daemon does not repeat the warning, but shows the card.
	m2 := New(health.Default())
	m2.SetWarned(m.Warned())
	if out := m2.Subs(now.Add(31*time.Hour), proto.NotifyImportant, list); len(out.Toasts)+len(out.Journal) != 0 || len(m2.Incidents()) != 1 {
		t.Errorf("после перезапуска службы: %+v, карточек %d", out, len(m2.Incidents()))
	}

	// The term passes: that is a new state and is told.
	out = m.Subs(now.Add(51*time.Hour), proto.NotifyImportant, list)
	if len(out.Toasts) != 1 || out.Toasts[0].ID != toast.ID || out.Toasts[0].Title != "Подписка «Основная» закончилась" {
		t.Errorf("срок вышел: %+v", out)
	}
	if !Expired(now.Add(51*time.Hour), list[0]) || Expired(now, list[0]) || Expired(now, list[2]) {
		t.Error("Expired отвечает неверно")
	}

	// Renewed: the card goes, the toast is taken down, nothing is trumpeted.
	list[0].ExpireAt = at(60 * 24 * time.Hour)
	out = m.Subs(now.Add(52*time.Hour), proto.NotifyImportant, list)
	if len(out.Toasts) != 1 || !out.Toasts[0].Close || len(m.Incidents()) != 0 || len(out.Journal) != 1 ||
		out.Journal[0].Text != "Подписка «Основная» снова действует." {
		t.Errorf("после продления: %+v", out)
	}

	// Traffic used up; at the silent level the journal still knows.
	list[1].Used = num(100 << 30)
	out = m.Subs(now.Add(53*time.Hour), proto.NotifySilent, list)
	if len(out.Toasts) != 0 || len(out.Journal) != 1 || !strings.Contains(out.Journal[0].Text, "закончился трафик") ||
		!strings.Contains(out.Journal[0].Text, "100,0 ГБ из 100,0 ГБ") {
		t.Errorf("трафик исчерпан, уровень «тихо»: %+v", out)
	}
	// A subscription that is removed takes its card along.
	if out := m.Subs(now.Add(54*time.Hour), proto.NotifyImportant, list[:1]); !out.Changed || len(m.Incidents()) != 0 {
		t.Errorf("подписку удалили: %+v, %+v", out, m.Incidents())
	}
}

// ---- wording ---------------------------------------------------------------

func TestSpanAndDays(t *testing.T) {
	for d, want := range map[time.Duration]string{
		40 * time.Second:                      "40 с",
		59*time.Second + 600*time.Millisecond: "1 мин",
		2*time.Minute + 10*time.Second:        "2 мин",
		59 * time.Minute:                      "59 мин",
		time.Hour:                             "1 ч",
		65 * time.Minute:                      "1 ч 5 мин",
	} {
		if got := span(d); got != want {
			t.Errorf("span(%v) = %q, жду %q", d, got, want)
		}
	}
	for n, want := range map[int]string{1: "1 день", 2: "2 дня", 3: "3 дня", 5: "5 дней", 11: "11 дней", 21: "21 день", 22: "22 дня"} {
		if got := days(n); got != want {
			t.Errorf("days(%d) = %q, жду %q", n, got, want)
		}
	}
}

func TestEveryHintAndVerdictHasWords(t *testing.T) {
	for _, h := range []health.Hint{health.HintUnreachable, health.HintKeys, health.HintAuth, health.HintBlocked, health.HintExpired} {
		if text := HintText(h); !strings.HasPrefix(text, "Похоже") {
			t.Errorf("подсказка %q: %q — должна подаваться как предположение", h, text)
		}
	}
	if HintText(health.HintNone) != "" || HintText(health.HintReset) != "" {
		t.Error("у отсутствующей подсказки есть текст")
	}
	in := Input{Server: Named{ID: "a-DE"}, FailText: "Причина."}
	for _, v := range []string{proto.VerdictServerDown, proto.VerdictAllServersDown, proto.VerdictNoInternet, proto.VerdictNoNetwork,
		proto.VerdictDegraded, proto.VerdictEngineFailed, proto.VerdictHelperDown} {
		if !trouble(v) {
			t.Errorf("%s не считается проблемой", v)
		}
		for _, unstable := range []bool{false, true} {
			title, body := describe(v, in, unstable)
			if title == "" || body == "" || title == "Проблема с VPN" {
				t.Errorf("%s (нестабильно=%v): заголовок %q, описание %q", v, unstable, title, body)
			}
		}
		if end := endText(v, in.Server, in.Server, false, time.Minute); end == "" || strings.HasPrefix(end, "Снова работает.") {
			t.Errorf("%s: у окончания нет своих слов: %q", v, end)
		}
		if len(actions(v, in)) == 0 {
			t.Errorf("%s: нет ни одной кнопки", v)
		}
	}
	for _, v := range []string{proto.VerdictOK, proto.VerdictSettling, proto.VerdictEngineRecovering} {
		if trouble(v) {
			t.Errorf("%s считается проблемой", v)
		}
	}
}

func TestIDsContinueAfterARestart(t *testing.T) {
	now := time.Date(2026, 11, 16, 23, 59, 0, 0, time.UTC)
	m := New(health.Default())
	m.SeedID("i-20261116-7")
	m.SeedID("i-20261116-3")
	m.SeedID("мусор")
	if id := m.nextID(now); id != "i-20261116-8" {
		t.Errorf("следующий id %q, жду i-20261116-8", id)
	}
	if id := m.nextID(now.Add(2 * time.Minute)); id != "i-20261117-1" {
		t.Errorf("первый id нового дня %q", id)
	}
}
