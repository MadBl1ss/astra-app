// Package incident turns the stream of verdicts into episodes a person cares
// about - "the server was down from 10:20 to 10:22" - and decides which of
// them deserve to interrupt somebody.
//
// Like the health automaton it is pure: time comes in with every call, and
// what goes out is data - journal lines, toasts, the list for State. The
// daemon does the writing and the sending.
package incident

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"strelka/internal/health"
	"strelka/proto"
)

// Named is a server as the user knows it.
type Named struct {
	ID   string
	Name string
}

func (n Named) title() string {
	if n.Name != "" {
		return n.Name
	}
	return n.ID
}

// Input is one look at the world.
type Input struct {
	Diag health.Diagnosis
	// Off: the VPN is off or on its way there because the user said so. An
	// open incident has lost its point.
	Off bool
	// Level is settings.notify.
	Level string
	// Server is the one the diagnosis is about, under its display name.
	Server Named
	// Hint overrides the diagnosis's own when the daemon knows better (a
	// subscription that ran out).
	Hint health.Hint
	// Switch is a live server to offer in "server down"; nil when there is none.
	Switch *Named
	// Moved: the daemon has already switched to this server (failover).
	Moved *Named
	// FailText words the reason of engine_failed.
	FailText string
	// Blocking: the kill switch has kept the internet closed for longer than
	// a restart takes. With engine_failed it changes the wording; with
	// anything else it is the trouble, whatever the verdict says.
	Blocking bool
}

// kindOf names the trouble an input stands for: the verdict, unless the
// kill switch is what the user feels.
func kindOf(in Input) string {
	if in.Blocking && in.Diag.Verdict != proto.VerdictEngineFailed {
		return proto.IncidentBlocked
	}
	return in.Diag.Verdict
}

// Entry is a line for the journal.
type Entry struct {
	Cat, Level, Text string
	Incident         string
}

// Output is what one call produced.
type Output struct {
	Journal []Entry
	Toasts  []proto.Toast
	// Changed: the list for State.incidents is not what it was.
	Changed bool
}

// episode is one incident from its opening to its end, reopenings included.
type episode struct {
	id       string
	kind     string // the verdict it currently stands for
	server   Named
	opened   time.Time // the first opening
	since    time.Time // the latest opening; what "не работало N" counts from
	closed   time.Time
	title    string
	body     string
	flaps    int
	reopens  []time.Time
	unstable bool
	// listed: it has, at some point, been a problem worth a card in the
	// window. It keeps the card until it ends.
	listed bool
	// notified: a toast about it is on record and its end has not been
	// announced yet. told: it was announced at least once in its life.
	notified bool
	told     bool
	// quietEnd: the daemon already dealt with it (failover); its end is not news.
	quietEnd bool
	// calm: "ok" has been seen since the last trouble.
	calm  bool
	toast string // what the last toast said, to tell an update from a repeat
}

// Manager keeps the incidents. It is owned by the daemon loop.
type Manager struct {
	t health.Tuning

	day string // the date part of the last id
	seq int

	cur        *episode // the open health incident
	last       *episode // the one that ended most recently
	okAt       time.Time
	graceUntil time.Time

	subs   map[string]*subIncident
	warned map[string]string
}

func New(t health.Tuning) *Manager {
	return &Manager{t: t, subs: map[string]*subIncident{}, warned: map[string]string{}}
}

// SeedID continues the numbering after an id found in the journal, so two
// runs of the daemon on one day do not hand out the same id.
func (m *Manager) SeedID(id string) {
	parts := strings.Split(id, "-")
	if len(parts) != 3 || parts[0] != "i" {
		return
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil {
		return
	}
	if parts[1] > m.day || (parts[1] == m.day && n > m.seq) {
		m.day, m.seq = parts[1], n
	}
}

func (m *Manager) nextID(now time.Time) string {
	if day := now.UTC().Format("20060102"); day != m.day {
		m.day, m.seq = day, 0
	}
	m.seq++
	return fmt.Sprintf("i-%s-%d", m.day, m.seq)
}

// UserAction opens the grace window: the user has just stopped, restarted,
// switched or refreshed something, and what breaks next is that.
func (m *Manager) UserAction(now time.Time) { m.graceUntil = now.Add(m.t.Grace) }

func trouble(kind string) bool {
	switch kind {
	case proto.IncidentBlocked:
		return true
	case proto.VerdictServerDown, proto.VerdictAllServersDown, proto.VerdictNoInternet, proto.VerdictNoNetwork,
		proto.VerdictDegraded, proto.VerdictEngineFailed, proto.VerdictHelperDown:
		return true
	}
	return false
}

// carded reports whether a kind is the VPN's own problem and gets a card in
// the window and a toast. No network and no internet the user sees without
// us; a shaky connection is a matter for the journal and the tray icon.
func carded(kind string) bool {
	switch kind {
	case proto.VerdictServerDown, proto.VerdictAllServersDown, proto.VerdictEngineFailed, proto.VerdictHelperDown,
		proto.IncidentBlocked:
		return true
	}
	return false
}

func severity(kind string) string {
	if kind == proto.VerdictEngineFailed || kind == proto.IncidentBlocked {
		return proto.SeverityCritical
	}
	return proto.SeverityProblem
}

func category(kind string) string {
	switch kind {
	case proto.VerdictServerDown, proto.VerdictAllServersDown, proto.VerdictDegraded:
		return proto.CatServer
	case proto.VerdictNoInternet, proto.VerdictNoNetwork:
		return proto.CatSystem
	case proto.IncidentBlocked:
		return proto.CatGuard
	}
	return proto.CatVPN
}

// Observe takes one look and moves the health incident along. The daemon
// calls it on every change and on every tick: several of the rules are
// about how long something has lasted.
func (m *Manager) Observe(now time.Time, in Input) Output {
	var out Output
	if in.Hint == health.HintNone {
		in.Hint = in.Diag.Hint
	}
	switch kind := kindOf(in); {
	case trouble(kind):
		m.okAt = time.Time{}
		m.troubled(now, in, &out)
	case kind == proto.VerdictOK:
		if m.cur == nil {
			break
		}
		m.cur.calm = true
		if m.okAt.IsZero() {
			m.okAt = now
		}
		hold := m.t.CloseAfterOK
		if m.cur.unstable {
			hold = m.t.CalmFor
		}
		if now.Sub(m.okAt) >= hold {
			m.end(now, in, &out, false)
		}
	default:
		// Settling and restarting are neither trouble nor health: "ok" has
		// to hold in one piece.
		m.okAt = time.Time{}
		if in.Off && m.cur != nil {
			m.end(now, in, &out, true)
		}
	}
	return out
}

func (m *Manager) troubled(now time.Time, in Input, out *Output) {
	kind := kindOf(in)
	if m.cur != nil {
		m.update(now, in, out)
		return
	}
	// What the user set off themselves is not an incident - unless it is
	// still there when the window closes. Three things do not wait: an engine
	// that will not start, an internet the kill switch has kept closed (it
	// has waited its own time already), and a switch the daemon has made.
	if now.Before(m.graceUntil) && kind != proto.VerdictEngineFailed && kind != proto.IncidentBlocked && in.Moved == nil {
		return
	}
	if e := m.last; e != nil && e.kind == kind && now.Sub(e.closed) <= m.t.ReopenWithin {
		m.reopen(now, in, out)
		return
	}
	e := &episode{id: m.nextID(now), kind: kind, server: in.Server, opened: now, since: now, listed: carded(kind)}
	e.title, e.body = describe(kind, in, false)
	e.quietEnd = in.Moved != nil
	m.cur, m.last = e, nil
	out.Journal = append(out.Journal, Entry{Cat: category(kind), Level: severity(kind), Text: sentence(e.title, e.body), Incident: e.id})
	out.Changed = out.Changed || e.listed
	m.announce(e, in, out)
}

// reopen brings the incident that ended a moment ago back instead of
// starting another: to the user it is the same trouble.
func (m *Manager) reopen(now time.Time, in Input, out *Output) {
	e := m.last
	m.cur, m.last = e, nil
	e.closed, e.since, e.calm = time.Time{}, now, false
	e.server = in.Server
	e.flaps++
	recent := e.reopens[:0]
	for _, at := range e.reopens {
		if now.Sub(at) < m.t.UnstableWindow {
			recent = append(recent, at)
		}
	}
	e.reopens = append(recent, now)
	if len(e.reopens) >= m.t.UnstableFlaps {
		e.unstable = true
	}
	e.title, e.body = describe(e.kind, in, e.unstable)
	text := reopenText(e.title, e.flaps)
	if e.unstable {
		text = sentence(e.title, e.body)
	}
	out.Journal = append(out.Journal, Entry{Cat: category(e.kind), Level: severity(e.kind), Text: text, Incident: e.id})
	out.Changed = out.Changed || e.listed
	// No toast: a reopening is not news, and an unstable connection is
	// reported once - by the toast its first fall already got.
}

// update keeps an open incident in step with the diagnosis: the cause may
// change, a hint may turn up, another server may be the dead one now.
func (m *Manager) update(now time.Time, in Input, out *Output) {
	e := m.cur
	kind := kindOf(in)
	if e.calm {
		// It had come good and fell again without ever closing.
		e.calm = false
		if e.unstable {
			e.flaps++
			out.Changed = out.Changed || e.listed
		}
	}
	title, body := describe(kind, in, e.unstable)
	// A switch the daemon made stays in the text until something else changes.
	if e.quietEnd && in.Moved == nil && kind == e.kind && in.Server.ID == e.server.ID {
		title, body = e.title, e.body
	}
	if kind == e.kind && title == e.title && body == e.body {
		return
	}
	if in.Moved != nil {
		e.quietEnd = true
	} else if kind != e.kind || in.Server.ID != e.server.ID {
		e.quietEnd = false
	}
	e.kind, e.server, e.title, e.body = kind, in.Server, title, body
	if carded(kind) && !e.listed {
		e.listed = true
	}
	out.Journal = append(out.Journal, Entry{Cat: category(kind), Level: severity(kind), Text: sentence(title, body), Incident: e.id})
	out.Changed = out.Changed || e.listed
	m.announce(e, in, out)
}

// announce sends the incident's toast when the rules allow one: the first
// time it is a problem worth a toast, and afterwards only to replace the
// toast that is already out.
func (m *Manager) announce(e *episode, in Input, out *Output) {
	if in.Level == proto.NotifySilent {
		return
	}
	if !e.notified && (e.told || !carded(e.kind)) {
		return
	}
	t := proto.Toast{
		ID: e.id, Severity: severity(e.kind), Title: e.title, Body: e.body,
		Actions: actions(e.kind, in), Sticky: severity(e.kind) == proto.SeverityCritical,
	}
	key := fmt.Sprint(t)
	if key == e.toast {
		return
	}
	e.toast, e.notified, e.told = key, true, true
	out.Toasts = append(out.Toasts, t)
}

// end closes the open incident. moot means the question went away (the VPN
// was switched off) rather than the trouble.
func (m *Manager) end(now time.Time, in Input, out *Output, moot bool) {
	e := m.cur
	m.cur, m.okAt = nil, time.Time{}
	e.closed = now
	out.Changed = out.Changed || e.listed
	if moot {
		out.Journal = append(out.Journal, Entry{Cat: category(e.kind), Level: proto.LevelInfo, Text: mootText(e.title), Incident: e.id})
		if e.notified {
			out.Toasts = append(out.Toasts, proto.Toast{ID: e.id, Actions: []proto.ToastAction{}, Close: true})
		}
		// What comes after the user switched the VPN back on is a new story.
		return
	}
	text := endText(e.kind, e.server, in.Server, e.unstable, now.Sub(e.since))
	out.Journal = append(out.Journal, Entry{Cat: category(e.kind), Level: proto.LevelInfo, Text: text, Incident: e.id})
	// The end is told only to those who were told the beginning.
	if e.notified && !e.quietEnd && in.Level != proto.NotifySilent {
		out.Toasts = append(out.Toasts, proto.Toast{
			ID: e.id, Severity: proto.SeverityInfo, Title: titleBack, Body: text, Actions: []proto.ToastAction{},
		})
	}
	e.notified, e.toast, e.unstable = false, "", false
	m.last = e
}

// Notice passes a piece of plain news ("VPN подключён") through the level
// setting: such things are shown only to those who asked for everything.
func (m *Manager) Notice(level string, t proto.Toast) []proto.Toast {
	if level != proto.NotifyAll {
		return nil
	}
	return []proto.Toast{t}
}

// Incidents is the list for State.incidents: open ones that have a card.
func (m *Manager) Incidents() []proto.Incident {
	out := []proto.Incident{}
	if e := m.cur; e != nil && e.listed {
		inc := proto.Incident{
			ID: e.id, Kind: e.kind, Severity: severity(e.kind), Opened: e.opened.UTC().Truncate(time.Second),
			Title: e.title, Body: e.body, Flaps: e.flaps, Unstable: e.unstable,
		}
		if e.server.ID != "" && (e.kind == proto.VerdictServerDown || e.kind == proto.VerdictDegraded) {
			id := e.server.ID
			inc.Server = &id
		}
		out = append(out, inc)
	}
	ids := make([]string, 0, len(m.subs))
	for id := range m.subs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := m.subs[id]
		out = append(out, proto.Incident{
			ID: s.id, Kind: s.kind, Severity: proto.SeverityProblem, Opened: s.opened.UTC().Truncate(time.Second),
			Title: s.title, Body: s.body,
		})
	}
	return out
}

// Open reports the id of the open health incident, "" when there is none.
func (m *Manager) Open() string {
	if m.cur == nil {
		return ""
	}
	return m.cur.id
}

// ---- subscriptions --------------------------------------------------------

// SubStatus is what a subscription's panel said about it at the last update.
type SubStatus struct {
	ID, Name    string
	ExpireAt    *time.Time
	Used, Total *int64
}

type subIncident struct {
	id, kind    string
	opened      time.Time
	title, body string
	notified    bool
}

func subKind(now time.Time, s SubStatus, warn time.Duration) string {
	switch {
	case s.ExpireAt != nil && !now.Before(*s.ExpireAt):
		return proto.IncidentSubExpired
	case s.Total != nil && s.Used != nil && *s.Total > 0 && *s.Used >= *s.Total:
		return proto.IncidentSubTraffic
	case s.ExpireAt != nil && s.ExpireAt.Sub(now) <= warn:
		return proto.IncidentSubExpiring
	}
	return ""
}

// Subs checks the subscriptions for ones that are about to end, have ended
// or have used their traffic up. Each such state is announced once - Warned
// carries that memory across restarts - and stays in State until it clears.
func (m *Manager) Subs(now time.Time, level string, list []SubStatus) Output {
	var out Output
	present := map[string]bool{}
	for _, s := range list {
		present[s.ID] = true
		kind := subKind(now, s, m.t.SubWarnBefore)
		have := m.subs[s.ID]
		if kind == "" {
			if have != nil {
				delete(m.subs, s.ID)
				out.Changed = true
				out.Journal = append(out.Journal, Entry{Cat: proto.CatSubs, Level: proto.LevelInfo, Text: subBackText(s.Name), Incident: have.id})
				if have.notified {
					out.Toasts = append(out.Toasts, proto.Toast{ID: have.id, Actions: []proto.ToastAction{}, Close: true})
				}
			}
			delete(m.warned, s.ID)
			continue
		}
		title, body := describeSub(kind, s, now)
		if have != nil && have.kind == kind {
			if have.title != title || have.body != body { // "осталось 2 дня" became "1 день"
				have.title, have.body = title, body
				out.Changed = true
			}
			continue
		}
		inc := &subIncident{kind: kind, opened: now, title: title, body: body}
		if have != nil {
			inc.id, inc.notified = have.id, have.notified
		} else {
			inc.id = m.nextID(now)
		}
		m.subs[s.ID] = inc
		out.Changed = true

		// The same state of the same term is told once, however many times
		// the daemon starts while it lasts.
		key := kind
		if s.ExpireAt != nil {
			key += "|" + s.ExpireAt.UTC().Format("2006-01-02")
		}
		if m.warned[s.ID] == key {
			continue
		}
		m.warned[s.ID] = key
		out.Journal = append(out.Journal, Entry{Cat: proto.CatSubs, Level: proto.LevelProblem, Text: sentence(title, body), Incident: inc.id})
		if level != proto.NotifySilent {
			inc.notified = true
			out.Toasts = append(out.Toasts, proto.Toast{
				ID: inc.id, Severity: proto.SeverityProblem, Title: title, Body: body,
				Actions: []proto.ToastAction{{ID: proto.ActionUpdateSubs, Label: labelUpdateSubs}, {ID: proto.ActionOpen, Label: labelOpen}},
			})
		}
	}
	for id, have := range m.subs {
		if !present[id] { // the subscription was removed; so is its trouble
			delete(m.subs, id)
			delete(m.warned, id)
			out.Changed = true
			if have.notified {
				out.Toasts = append(out.Toasts, proto.Toast{ID: have.id, Actions: []proto.ToastAction{}, Close: true})
			}
		}
	}
	return out
}

// Warned is what has been announced about each subscription; the daemon
// saves it and gives it back through SetWarned after a restart.
func (m *Manager) Warned() map[string]string {
	out := make(map[string]string, len(m.warned))
	for k, v := range m.warned {
		out[k] = v
	}
	return out
}

func (m *Manager) SetWarned(w map[string]string) {
	m.warned = map[string]string{}
	for k, v := range w {
		m.warned[k] = v
	}
}

// Expired reports whether the subscription has run out one way or the
// other: the best explanation there is for its servers going silent.
func Expired(now time.Time, s SubStatus) bool {
	k := subKind(now, s, 0)
	return k == proto.IncidentSubExpired || k == proto.IncidentSubTraffic
}
