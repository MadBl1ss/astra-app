package journal

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"strelka/proto"
)

var now = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func TestSurvivesATornLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j := Open(path)
	j.Add(now, proto.CatSystem, proto.LevelInfo, "первая", "")
	j.Add(now, proto.CatVPN, proto.LevelProblem, "вторая", "i-20261112-1")
	j.Close()

	// Power went out in the middle of a write.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"id":3,"t":"2026-11-12T10:00:0`)
	f.Close()

	j = Open(path)
	defer j.Close()
	if j.Len() != 2 || j.List(proto.JournalListParams{}).Entries[0].Text != "вторая" {
		t.Fatalf("прочитано %d записей: %+v", j.Len(), j.List(proto.JournalListParams{}))
	}
	if j.LastIncident() != "i-20261112-1" {
		t.Errorf("последний инцидент %q", j.LastIncident())
	}
	if e := j.Add(now, proto.CatVPN, proto.LevelInfo, "третья", ""); e.ID != 3 || e.Incident != nil {
		t.Errorf("новая запись: %+v, жду id 3 без инцидента", e)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Count(raw, []byte("\n"))
}

func TestFileIsTrimmedToTheLastEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j := Open(path)
	total := Keep + slack + 120
	for i := 1; i <= total; i++ {
		j.Add(now, proto.CatVPN, proto.LevelInfo, fmt.Sprintf("запись %d", i), "")
	}
	if j.Len() != Keep {
		t.Errorf("в памяти %d записей, предел %d", j.Len(), Keep)
	}
	if n := countLines(t, path); n > Keep+slack || n < Keep {
		t.Errorf("в файле %d строк, жду от %d до %d", n, Keep, Keep+slack)
	}
	newest := j.List(proto.JournalListParams{Limit: 1}).Entries[0]
	if newest.ID != int64(total) || newest.Text != fmt.Sprintf("запись %d", total) {
		t.Errorf("самая свежая запись: %+v", newest)
	}
	j.Close()

	// A new run sees the same history and keeps counting.
	j = Open(path)
	defer j.Close()
	if j.Len() != Keep {
		t.Errorf("после перезапуска %d записей", j.Len())
	}
	if e := j.Add(now, proto.CatVPN, proto.LevelInfo, "ещё одна", ""); e.ID != int64(total)+1 {
		t.Errorf("id после перезапуска %d, жду %d", e.ID, total+1)
	}
	oldest := j.List(proto.JournalListParams{Before: int64(total) - Keep + 3, Limit: 10})
	if len(oldest.Entries) != 1 || oldest.More {
		t.Errorf("хвост журнала: %+v", oldest)
	}
}

// A file left by a version without trimming is cut down when it is opened.
func TestOversizedFileIsTrimmedOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	var buf bytes.Buffer
	for i := 1; i <= Keep+slack+50; i++ {
		fmt.Fprintf(&buf, `{"id":%d,"t":"2026-11-12T10:00:00Z","cat":"vpn","level":"info","text":"старая","incident":null}`+"\n", i)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	j := Open(path)
	defer j.Close()
	if n := countLines(t, path); n != Keep {
		t.Errorf("после открытия в файле %d строк, жду %d", n, Keep)
	}
	if e := j.Add(now, proto.CatVPN, proto.LevelInfo, "новая", ""); e.ID != Keep+slack+51 {
		t.Errorf("id новой записи %d", e.ID)
	}
	if n := countLines(t, path); n != Keep+1 {
		t.Errorf("новая запись не дописана в урезанный файл: строк %d", n)
	}
}

func TestListPagesAndFilters(t *testing.T) {
	j := Open(filepath.Join(t.TempDir(), "journal.jsonl"))
	defer j.Close()
	for i := 1; i <= 250; i++ {
		cat, level, inc := proto.CatVPN, proto.LevelInfo, ""
		switch {
		case i%10 == 0:
			cat, level, inc = proto.CatServer, proto.LevelProblem, "i-20261112-1"
		case i%25 == 1:
			cat, level = proto.CatSubs, proto.LevelCritical
		}
		j.Add(now, cat, level, fmt.Sprintf("запись %d", i), inc)
	}

	// Without parameters: the newest hundred, newest first.
	page := j.List(proto.JournalListParams{})
	if len(page.Entries) != DefaultLimit || !page.More || page.Entries[0].ID != 250 || page.Entries[99].ID != 151 {
		t.Fatalf("первая страница: %d записей, more=%v, от %d до %d", len(page.Entries), page.More, page.Entries[0].ID, page.Entries[len(page.Entries)-1].ID)
	}
	// Paging by the last id seen reaches the end without gaps or repeats.
	seen := len(page.Entries)
	for page.More {
		page = j.List(proto.JournalListParams{Before: page.Entries[len(page.Entries)-1].ID})
		seen += len(page.Entries)
	}
	if seen != 250 || page.Entries[len(page.Entries)-1].ID != 1 {
		t.Errorf("постранично прочитано %d записей, последняя %d", seen, page.Entries[len(page.Entries)-1].ID)
	}

	problems := j.List(proto.JournalListParams{Filter: &proto.JournalFilter{Levels: []string{proto.LevelProblem, proto.LevelCritical}}})
	if len(problems.Entries) != 35 || problems.More {
		t.Errorf("проблем и критичных %d, жду 35", len(problems.Entries))
	}
	servers := j.List(proto.JournalListParams{Limit: 10, Filter: &proto.JournalFilter{Cats: []string{proto.CatServer}}})
	if len(servers.Entries) != 10 || !servers.More || servers.Entries[0].ID != 250 || servers.Entries[9].ID != 160 {
		t.Errorf("категория server, страница в 10: %+v", servers)
	}
	one := j.List(proto.JournalListParams{Filter: &proto.JournalFilter{Incident: "i-20261112-1", Cats: []string{proto.CatVPN}}})
	if len(one.Entries) != 0 || one.More || one.Entries == nil {
		t.Errorf("фильтры складываются: %+v", one)
	}
	if got := j.List(proto.JournalListParams{Limit: 100000}); len(got.Entries) != 250 {
		t.Errorf("limit выше предела: %d записей", len(got.Entries))
	}
	inc := j.List(proto.JournalListParams{Filter: &proto.JournalFilter{Incident: "i-20261112-1"}})
	if len(inc.Entries) != 25 {
		t.Errorf("записей инцидента %d, жду 25", len(inc.Entries))
	}
}
