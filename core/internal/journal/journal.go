// Package journal is the user-facing event log: what happened to the VPN,
// in words, newest first. It lives in journal.jsonl, one entry per line, and
// in memory for the window to page through.
package journal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"time"

	"strelka/internal/store"
	"strelka/proto"
)

const (
	// Keep is how many entries survive. At a few dozen a day that is months
	// of history in about a megabyte.
	Keep = 5000
	// slack is how far the file may grow past Keep before it is rewritten.
	// Trimming on every append would rewrite a megabyte per entry.
	slack = 500

	// DefaultLimit and MaxLimit bound one page of List.
	DefaultLimit = 100
	MaxLimit     = 500
)

// Journal is owned by the daemon loop and needs no locking.
type Journal struct {
	path    string
	entries []proto.JournalEntry // oldest first, at most Keep
	nextID  int64
	f       *os.File
	lines   int // lines in the file, torn ones included
}

// Open reads what an earlier run left, so ids keep growing across restarts
// and the history is there before anything new happens.
func Open(path string) *Journal {
	j := &Journal{path: path, nextID: 1}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 4096), 1<<20)
		for sc.Scan() {
			j.lines++
			var e proto.JournalEntry
			// A torn last line after a power loss is skipped, not fatal.
			if json.Unmarshal(sc.Bytes(), &e) != nil || e.ID == 0 {
				continue
			}
			j.keep(e)
			if e.ID >= j.nextID {
				j.nextID = e.ID + 1
			}
		}
		f.Close()
	}
	if j.lines > Keep+slack {
		j.compact()
	}
	j.open()
	return j
}

func (j *Journal) open() {
	// Losing the file is survivable: the entries in memory still work.
	j.f, _ = os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func (j *Journal) keep(e proto.JournalEntry) {
	j.entries = append(j.entries, e)
	if len(j.entries) > Keep {
		j.entries = append(j.entries[:0], j.entries[len(j.entries)-Keep:]...)
	}
}

// compact rewrites the file with the entries that are kept. The file must
// not be open: Windows will not replace an open file.
func (j *Journal) compact() {
	var buf bytes.Buffer
	for _, e := range j.entries {
		line, err := json.Marshal(e)
		if err != nil {
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := store.WriteAtomic(j.path, buf.Bytes(), 0o600); err == nil {
		j.lines = len(j.entries)
	}
}

// Add appends an entry. incident is the id of the incident it belongs to,
// or "".
func (j *Journal) Add(now time.Time, cat, level, text, incident string) proto.JournalEntry {
	e := proto.JournalEntry{ID: j.nextID, T: now.UTC().Truncate(time.Millisecond), Cat: cat, Level: level, Text: text}
	if incident != "" {
		e.Incident = &incident
	}
	j.nextID++
	j.keep(e)
	if j.f != nil {
		if line, err := json.Marshal(e); err == nil {
			j.f.Write(append(line, '\n'))
			j.lines++
		}
	}
	if j.lines > Keep+slack && j.f != nil {
		j.f.Close()
		j.compact()
		j.open()
	}
	return e
}

func has(list []string, v string) bool {
	if len(list) == 0 {
		return true
	}
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func matches(e proto.JournalEntry, f *proto.JournalFilter) bool {
	if f == nil {
		return true
	}
	if !has(f.Cats, e.Cat) || !has(f.Levels, e.Level) {
		return false
	}
	return f.Incident == "" || (e.Incident != nil && *e.Incident == f.Incident)
}

// List returns one page, newest first. More says whether older entries that
// match are left.
func (j *Journal) List(p proto.JournalListParams) proto.JournalListResult {
	limit := p.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	res := proto.JournalListResult{Entries: []proto.JournalEntry{}}
	for i := len(j.entries) - 1; i >= 0; i-- {
		e := j.entries[i]
		if (p.Before > 0 && e.ID >= p.Before) || !matches(e, p.Filter) {
			continue
		}
		if len(res.Entries) == limit {
			res.More = true
			break
		}
		res.Entries = append(res.Entries, e)
	}
	return res
}

// LastIncident is the id of the most recent incident on record, "" if none:
// the numbering of a new run continues from it.
func (j *Journal) LastIncident() string {
	for i := len(j.entries) - 1; i >= 0; i-- {
		if id := j.entries[i].Incident; id != nil {
			return *id
		}
	}
	return ""
}

// Len is how many entries are held.
func (j *Journal) Len() int { return len(j.entries) }

func (j *Journal) Close() {
	if j.f != nil {
		j.f.Close()
		j.f = nil
	}
}
