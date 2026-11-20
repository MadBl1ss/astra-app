package stats

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func answered(ms int, at time.Time) Answer { return Answer{OK: true, Ms: ms, At: at} }

var silent = Answer{}

func TestOrderPutsFastAndSteadyFirst(t *testing.T) {
	st := &Stats{Servers: map[string]*Stat{
		"a-AT": {Avg: 400, N: 9},
		"a-DE": {Avg: 70, N: 9},
		"a-FI": {Avg: 60, Miss: 0.5, N: 9}, // quickest when it answers, which is half the time
		"a-NL": {Avg: 90, N: 9},
		"a-SE": {Miss: 1}, // never answered: no rating at all
	}}
	pool := []string{"a-AT", "a-DE", "a-FI", "a-NEW", "a-NL", "a-SE"}
	got := st.Order(pool)
	want := []string{"a-DE", "a-NL", "a-FI", "a-AT", "a-NEW", "a-SE"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("порядок:\n есть %v\n жду  %v", got, want)
	}
	if pool[0] != "a-AT" {
		t.Error("Order испортил исходный список")
	}
	if got := (&Stats{}).Order(pool); !reflect.DeepEqual(got, pool) {
		t.Errorf("без статистики порядок должен остаться прежним: %v", got)
	}
}

func TestRoundsBuildThePicture(t *testing.T) {
	st := &Stats{Servers: map[string]*Stat{}}
	t0 := time.Unix(1_000_000, 0)

	// First round: both answered.
	px := map[string]Answer{"a-DE": answered(100, t0.Add(time.Second)), "a-NL": answered(200, t0.Add(time.Second))}
	if !st.Round(t0.Add(2*time.Second), px) {
		t.Fatal("первые результаты не учтены")
	}
	// Folding the same results again adds nothing.
	if st.Round(t0.Add(3*time.Second), px) {
		t.Error("тот же результат посчитан дважды")
	}
	if s := st.Servers["a-DE"]; s.N != 1 || s.Avg != 100 {
		t.Errorf("a-DE после одного ответа: %+v", s)
	}

	// Next round, two minutes on: DE got slower, NL fell silent.
	t1 := t0.Add(2 * time.Minute)
	px = map[string]Answer{"a-DE": answered(200, t1.Add(time.Second)), "a-NL": silent}
	st.Round(t1.Add(5*time.Second), px)
	if s := st.Servers["a-DE"]; s.N != 2 || s.Avg <= 100 || s.Avg >= 200 {
		t.Errorf("a-DE после 100 и 200 мс: среднее %.0f, жду между ними", s.Avg)
	}
	nl := st.Servers["a-NL"]
	if nl.Miss <= 0 {
		t.Fatalf("a-NL промолчал, а промах не записан: %+v", nl)
	}
	// A second round right away is the same miss, not another one.
	miss := nl.Miss
	t2 := t1.Add(10 * time.Second)
	px = map[string]Answer{"a-DE": answered(190, t2.Add(time.Second)), "a-NL": silent}
	st.Round(t2.Add(5*time.Second), px)
	if nl.Miss != miss {
		t.Errorf("один и тот же промах посчитан дважды: %.2f -> %.2f", miss, nl.Miss)
	}

	if r, _ := st.Rating("a-NL"); r <= 200 {
		t.Errorf("оценка a-NL %.0f: промах должен был её ухудшить", r)
	}
}

// With nobody in the round answering there is no telling a dead server from
// a dead network; a server the round never got to is not judged at all.
func TestNoMissesWithoutEvidenceTheNetworkWorks(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	st := &Stats{Servers: map[string]*Stat{}}
	st.Round(now, map[string]Answer{"a-DE": silent, "a-NL": silent})
	for tag, s := range st.Servers {
		if s.Miss > 0 {
			t.Errorf("вся сеть лежит, а %s записан промах %.2f", tag, s.Miss)
		}
	}
	st.Round(now, map[string]Answer{"a-DE": answered(80, now), "a-NL": silent})
	if s := st.Servers["a-NL"]; s == nil || s.Miss <= 0 {
		t.Errorf("a-DE ответил, значит сеть есть, а промах a-NL не записан: %+v", s)
	}
	if _, ok := st.Servers["a-FI"]; ok {
		t.Error("сервер, до которого проверка не дошла, попал в статистику")
	}
}

func TestSaveLoadAndForget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	now := time.Unix(1_000_000, 0)
	st := Load(path) // no file yet
	st.Servers["a-DE"] = &Stat{Avg: 70, N: 3, Seen: now}
	st.Servers["a-OLD"] = &Stat{Avg: 70, N: 3, Seen: now.Add(-2 * keep)}
	if err := st.Save(path, now); err != nil {
		t.Fatal(err)
	}
	got := Load(path)
	if s := got.Servers["a-DE"]; s == nil || s.Avg != 70 || s.N != 3 {
		t.Errorf("a-DE после записи и чтения: %+v", s)
	}
	if _, ok := got.Servers["a-OLD"]; ok {
		t.Error("сервер, которого нет уже два месяца, не забыт")
	}
}
