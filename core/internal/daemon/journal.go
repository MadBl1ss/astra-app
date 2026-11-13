package daemon

import (
	"bufio"
	"encoding/json"
	"os"
	"time"

	"strelka/proto"
)

// journalRing is how many entries are kept in memory for a client that has
// just connected.
const journalRing = 500

// journal is the user-facing event log: a ring in memory and an append-only
// journal.jsonl. It is owned by the daemon loop and needs no locking.
//
// Stage 03 moves it to its own package together with filters and rotation.
type journal struct {
	entries []proto.JournalEntry
	nextID  int64
	f       *os.File
}

// openJournal reads what an earlier run left, so ids keep growing across
// restarts and the last entries are there before anything new happens.
func openJournal(path string) *journal {
	j := &journal{nextID: 1}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 4096), 1<<20)
		for sc.Scan() {
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
	// Losing the file is survivable: the ring still works.
	j.f, _ = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	return j
}

func (j *journal) keep(e proto.JournalEntry) {
	j.entries = append(j.entries, e)
	if len(j.entries) > journalRing {
		j.entries = append(j.entries[:0], j.entries[len(j.entries)-journalRing:]...)
	}
}

func (j *journal) add(now time.Time, cat, level, text string) proto.JournalEntry {
	e := proto.JournalEntry{ID: j.nextID, T: now.UTC().Truncate(time.Millisecond), Cat: cat, Level: level, Text: text}
	j.nextID++
	j.keep(e)
	if j.f != nil {
		if line, err := json.Marshal(e); err == nil {
			j.f.Write(append(line, '\n'))
		}
	}
	return e
}

func (j *journal) close() {
	if j.f != nil {
		j.f.Close()
		j.f = nil
	}
}
