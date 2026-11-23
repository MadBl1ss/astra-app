package stats

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func answered(ms int, at time.Time) Answer { return Answer{OK: true, Ms: ms, At: at} }

var silent = Answer{}

var t0 = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// feed gives a server n answers of ms, one every step from start.
func feed(st *Stats, tag string, start time.Time, step time.Duration, n int, ms ...int) time.Time {
	at := start
	for i := range n {
		st.Answered(tag, ms[i%len(ms)], at)
		at = at.Add(step)
	}
	return at
}

func TestOrderPutsFastAndSteadyFirst(t *testing.T) {
	st := &Stats{Servers: map[string]*Stat{}}
	// Nine answers each, an hour apart: plenty of evidence for everyone.
	end := t0
	for tag, ms := range map[string]int{"a-AT": 400, "a-DE": 70, "a-NL": 90} {
		end = feed(st, tag, t0, time.Hour, 9, ms)
	}
	// FI is the quickest when it answers, which is half the time: slow is
	// better than silent.
	for i := range 9 {
		at := t0.Add(time.Duration(i) * time.Hour)
		st.Answered("a-FI", 60, at)
		st.Missed("a-FI", at.Add(30*time.Minute))
	}
	st.Missed("a-SE", t0) // never answered: no rating at all
	pool := []string{"a-AT", "a-DE", "a-FI", "a-NEW", "a-NL", "a-SE"}
	got := st.Order(pool, end)
	want := []string{"a-DE", "a-NL", "a-AT", "a-FI", "a-NEW", "a-SE"}
	if !reflect.DeepEqual(got, want) {
		for _, tag := range pool {
			t.Logf("%s: %+v", tag, st.Quality(tag, end))
		}
		t.Errorf("порядок:\n есть %v\n жду  %v", got, want)
	}
	if pool[0] != "a-AT" {
		t.Error("Order испортил исходный список")
	}
	if got := (&Stats{}).Order(pool, end); !reflect.DeepEqual(got, pool) {
		t.Errorf("без статистики порядок должен остаться прежним: %v", got)
	}
}

func TestRoundsBuildThePicture(t *testing.T) {
	st := &Stats{Servers: map[string]*Stat{}}

	// First round: both answered.
	px := map[string]Answer{"a-DE": answered(100, t0.Add(time.Second)), "a-NL": answered(200, t0.Add(time.Second))}
	if !st.Round(t0.Add(2*time.Second), px) {
		t.Fatal("первые результаты не учтены")
	}
	// Folding the same results again adds nothing.
	if st.Round(t0.Add(3*time.Second), px) {
		t.Error("тот же результат посчитан дважды")
	}
	if s := st.Servers["a-DE"]; s.N != 1 || s.Mean != 100 || s.W != 1 {
		t.Errorf("a-DE после одного ответа: %+v", s)
	}

	// Next round, twenty minutes on: DE got slower, NL fell silent.
	t1 := t0.Add(20 * time.Minute)
	px = map[string]Answer{"a-DE": answered(200, t1.Add(time.Second)), "a-NL": silent}
	st.Round(t1.Add(5*time.Second), px)
	if s := st.Servers["a-DE"]; s.N != 2 || s.Mean <= 100 || s.Mean >= 200 || s.Dev <= 0 {
		t.Errorf("a-DE после 100 и 200 мс: %+v, жду среднее между ними и разброс", s)
	}
	nl := st.Servers["a-NL"]
	if nl.MissW <= 0 {
		t.Fatalf("a-NL промолчал, а промах не записан: %+v", nl)
	}
	// A second round right away is nearly the same miss, not another one.
	miss := nl.MissW
	t2 := t1.Add(10 * time.Second)
	px = map[string]Answer{"a-DE": answered(190, t2.Add(time.Second)), "a-NL": silent}
	st.Round(t2.Add(5*time.Second), px)
	if nl.MissW > miss+0.05 {
		t.Errorf("промах через десять секунд весит как отдельный: %.2f -> %.2f", miss, nl.MissW)
	}

	now := t2.Add(time.Minute)
	if de, nlq := st.Quality("a-DE", now), st.Quality("a-NL", now); nlq.Score >= de.Score || nlq.Loss <= 0 {
		t.Errorf("a-NL с промахом (%.0f) не ниже a-DE (%.0f)", nlq.Score, de.Score)
	}
}

// The selected server is probed twice a minute. A day of that must not
// weigh more than a day of hourly tests by much more than the spacing says,
// and an hour of it must not wash away a week.
func TestFrequentProbesShareTheirWeight(t *testing.T) {
	st := &Stats{Servers: map[string]*Stat{}}
	feed(st, "a-DE", t0, time.Hour, 24, 80)
	before := st.Servers["a-DE"].Mean
	feed(st, "a-DE", t0.Add(24*time.Hour), 30*time.Second, 120, 300) // an hour of a bad mood
	s := st.Servers["a-DE"]
	if s.Mean > before+0.5*(300-before) {
		t.Errorf("час частых проверок перевесил сутки: было %.0f мс, стало %.0f", before, s.Mean)
	}
	if s.N != 144 {
		t.Errorf("ответов посчитано %d, жду 144", s.N)
	}
}

func TestOldEvidenceFades(t *testing.T) {
	st := &Stats{Servers: map[string]*Stat{}}
	feed(st, "a-DE", t0, time.Hour, 20, 80)
	early := st.Quality("a-DE", t0.Add(20*time.Hour))
	late := st.Quality("a-DE", t0.Add(20*time.Hour+3*halfLife))
	if late.Trust >= early.Trust {
		t.Errorf("доверие не падает со временем: %.2f -> %.2f", early.Trust, late.Trust)
	}
	if late.Ping != early.Ping {
		t.Errorf("пинг сам по себе поменялся: %.0f -> %.0f", early.Ping, late.Ping)
	}
	// A week later the server answers badly once: that answer counts for a
	// lot more than it would have the next minute.
	st.Answered("a-DE", 400, t0.Add(7*24*time.Hour))
	if m := st.Servers["a-DE"].Mean; m < 120 {
		t.Errorf("свежий ответ через неделю почти не сдвинул среднее: %.0f", m)
	}
}

func TestEvidenceBuildsTrust(t *testing.T) {
	st := &Stats{Servers: map[string]*Stat{}}
	st.Answered("a-LUCKY", 30, t0)
	feed(st, "a-SOLID", t0, time.Hour, 30, 60, 70, 65)
	now := t0.Add(30 * time.Hour)
	lucky, solid := st.Quality("a-LUCKY", now), st.Quality("a-SOLID", now)
	if lucky.Score >= solid.Score {
		t.Errorf("один удачный ответ (%.0f) обогнал тридцать хороших (%.0f)", lucky.Score, solid.Score)
	}
	if lucky.Trust >= 0.5 || solid.Trust < 0.85 {
		t.Errorf("доверие: один ответ %.2f, тридцать %.2f", lucky.Trust, solid.Trust)
	}
	if lucky.Jitter <= 0 {
		t.Error("у сервера с одним ответом разброс не предполагается, а он считается нулевым")
	}
}

func TestStableNeedsDaysAndEvenness(t *testing.T) {
	st := &Stats{Servers: map[string]*Stat{}}
	// Every hour for four days, evenly.
	end := feed(st, "a-EVEN", t0, time.Hour, 96, 60, 64, 58, 62)
	// The same number of answers in one afternoon.
	feed(st, "a-ONEDAY", t0, 2*time.Minute, 96, 60, 64, 58, 62)
	// Four days, but jumping between 40 and 400.
	feed(st, "a-JUMPY", t0, time.Hour, 96, 40, 400)
	even, oneday, jumpy := st.Quality("a-EVEN", end), st.Quality("a-ONEDAY", end), st.Quality("a-JUMPY", end)
	if !even.Stable || even.Days < 4 {
		t.Errorf("ровный сервер за четыре дня не стабилен: %+v", even)
	}
	if oneday.Stable {
		t.Errorf("сервер, проверенный за один день, назван стабильным: %+v", oneday)
	}
	if jumpy.Stable {
		t.Errorf("скачущий сервер назван стабильным: %+v", jumpy)
	}
	if jumpy.Score >= even.Score {
		t.Errorf("скачущий (%.0f) не ниже ровного (%.0f)", jumpy.Score, even.Score)
	}
}

func TestDaysWindow(t *testing.T) {
	s := &Stat{}
	for _, d := range []int{0, 1, 3} {
		s.markDay(t0.Add(time.Duration(d) * 24 * time.Hour))
	}
	s.markDay(t0.Add(2 * 24 * time.Hour)) // late news about an earlier day
	st := &Stats{Servers: map[string]*Stat{"x": s}}
	s.N, s.W = 1, 1
	if q := st.Quality("x", t0.Add(3*24*time.Hour)); q.Days != 4 {
		t.Errorf("дней с ответами %d, жду 4", q.Days)
	}
	if q := st.Quality("x", t0.Add(16*24*time.Hour)); q.Days != 1 {
		t.Errorf("через две недели после третьего дня в окне %d дней, жду 1", q.Days)
	}
	if q := st.Quality("x", t0.Add(40*24*time.Hour)); q.Days != 0 {
		t.Errorf("через месяц дней с ответами %d", q.Days)
	}
}

func TestSpeedIsAveragedByFactors(t *testing.T) {
	st := &Stats{Servers: map[string]*Stat{}}
	feed(st, "a-DE", t0, time.Hour, 10, 70)
	feed(st, "a-NL", t0, time.Hour, 10, 70)
	if !st.Speed("a-DE", 10, t0.Add(time.Hour)) || !st.Speed("a-DE", 90, t0.Add(2*time.Hour)) {
		t.Fatal("замер скорости не учтён")
	}
	if st.Speed("a-DE", 90, t0.Add(2*time.Hour)) {
		t.Error("тот же замер посчитан дважды")
	}
	now := t0.Add(10 * time.Hour)
	q := st.Quality("a-DE", now)
	if q.Mbps < 28 || q.Mbps > 32 {
		t.Errorf("10 и 90 Мбит/с дали %.1f, жду около 30", q.Mbps)
	}
	if st.Servers["a-DE"].Last != 90 {
		t.Errorf("последний замер %.0f", st.Servers["a-DE"].Last)
	}
	st.Speed("a-NL", 3, t0.Add(time.Hour))
	if de, nl := st.Quality("a-DE", now), st.Quality("a-NL", now); nl.Score >= de.Score {
		t.Errorf("медленный (%.0f) не ниже быстрого (%.0f) при равном пинге", nl.Score, de.Score)
	}
	// Speed alone is something known about a server.
	st.Speed("a-SPEEDONLY", 50, t0)
	if q := st.Quality("a-SPEEDONLY", now); !q.Known || q.Mbps != 50 || q.Ping != 0 {
		t.Errorf("сервер только с замером скорости: %+v", q)
	}
}

// With nobody in the round answering there is no telling a dead server from
// a dead network; a server the round never got to is not judged at all.
func TestNoMissesWithoutEvidenceTheNetworkWorks(t *testing.T) {
	now := t0
	st := &Stats{Servers: map[string]*Stat{}}
	st.Round(now, map[string]Answer{"a-DE": silent, "a-NL": silent})
	for tag, s := range st.Servers {
		if s.MissW > 0 {
			t.Errorf("вся сеть лежит, а %s записан промах %.2f", tag, s.MissW)
		}
	}
	st.Round(now, map[string]Answer{"a-DE": answered(80, now), "a-NL": silent})
	if s := st.Servers["a-NL"]; s == nil || s.MissW <= 0 {
		t.Errorf("a-DE ответил, значит сеть есть, а промах a-NL не записан: %+v", s)
	}
	if _, ok := st.Servers["a-FI"]; ok {
		t.Error("сервер, до которого проверка не дошла, попал в статистику")
	}
}

func TestSaveLoadAndForget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	now := t0
	st := Load(path) // no file yet
	feed(st, "a-DE", now.Add(-3*time.Hour), time.Hour, 3, 70)
	st.Speed("a-DE", 42, now)
	feed(st, "a-OLD", now.Add(-2*keep), time.Hour, 3, 70)
	want := *st.Servers["a-DE"]
	if err := st.Save(path, now); err != nil {
		t.Fatal(err)
	}
	got := Load(path)
	if s := got.Servers["a-DE"]; s == nil || !reflect.DeepEqual(*s, want) {
		t.Errorf("a-DE после записи и чтения:\n есть %+v\n жду  %+v", s, want)
	}
	if _, ok := got.Servers["a-OLD"]; ok {
		t.Error("сервер, которого нет уже два месяца, не забыт")
	}
}

// stats.json of 0.1.3, and the old tray's server-stats.json, have a moving
// average and a share of misses. They come over as evidence of about five
// answers, with the same delay and the same share of misses.
func TestVersionOneComesOver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	body := `{"servers":{
		"klex-NL":{"avg":40,"miss":0,"n":9,"seen":"2026-11-10T10:00:00Z"},
		"lider-DE":{"avg":300,"miss":0.2,"n":30,"seen":"2026-11-10T10:00:00Z","miss_at":"2026-11-10T11:00:00Z"},
		"dead-SE":{"miss":1,"n":0,"seen":"0001-01-01T00:00:00Z","miss_at":"2026-11-10T11:00:00Z"}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st := Load(path)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	nl, de, se := st.Quality("klex-NL", now), st.Quality("lider-DE", now), st.Quality("dead-SE", now)
	if nl.Ping != 40 || nl.Loss != 0 || nl.Days != 1 {
		t.Errorf("klex-NL: %+v", nl)
	}
	if de.Ping != 300 || de.Loss < 0.19 || de.Loss > 0.21 {
		t.Errorf("lider-DE: пинг %.0f, потери %.2f, жду 300 и 0.2", de.Ping, de.Loss)
	}
	if !se.Known || se.Ping != 0 || se.Loss != 1 {
		t.Errorf("dead-SE: %+v", se)
	}
	if order := st.Order([]string{"dead-SE", "lider-DE", "klex-NL"}, now); !reflect.DeepEqual(order, []string{"klex-NL", "lider-DE", "dead-SE"}) {
		t.Errorf("порядок после переноса: %v", order)
	}
	// Written back, it is in the current form and reads the same.
	if err := st.Save(path, now); err != nil {
		t.Fatal(err)
	}
	if again := Load(path).Quality("lider-DE", now); again != de {
		t.Errorf("после записи lider-DE читается иначе:\n было  %+v\n стало %+v", de, again)
	}
}
