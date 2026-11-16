// Package profile is what the user has: subscriptions, their servers, the
// chosen server, the mode, the favourites and this machine's device id. It
// is kept in profile.json and changed only by the daemon loop; every method
// here is plain data work with no I/O besides Load and Save.
package profile

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"strelka/internal/store"
	"strelka/internal/subs"
	"strelka/proto"
)

// Sub is one subscription.
type Sub struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// AutoName marks a name the user did not choose; the panel's own title
	// replaces it on the first download.
	AutoName bool `json:"autoName,omitempty"`
	// Prefix starts the ids of this subscription's servers ("lider-DE"). It
	// is fixed when the subscription is added: renaming a subscription must
	// not rename its servers, or the selection and the favourites would be
	// lost. Empty only for a lone unnamed subscription carried over from the
	// old Strelka, which never had one.
	Prefix string `json:"prefix"`
	URL    string `json:"url"`

	UpdatedAt *time.Time `json:"updatedAt,omitempty"` // the last successful download
	TriedAt   *time.Time `json:"triedAt,omitempty"`   // the last attempt, good or bad
	Error     string     `json:"error,omitempty"`     // why the last attempt failed

	ExpireAt   *time.Time `json:"expireAt,omitempty"`
	UsedBytes  *int64     `json:"usedBytes,omitempty"`
	TotalBytes *int64     `json:"totalBytes,omitempty"`
}

// Server is a server together with where it came from.
type Server struct {
	subs.Server
	Sub string `json:"sub"` // subscription id; "" for a server added by link
}

// Profile is the content of profile.json.
type Profile struct {
	HWID      string   `json:"hwid"`
	Subs      []Sub    `json:"subs"`
	Servers   []Server `json:"servers"`
	Selected  string   `json:"selected"`
	Mode      string   `json:"mode"`
	Favorites []string `json:"favorites"` // server ids, in the user's order
	NextSub   int      `json:"nextSub"`
}

// Errors the daemon turns into protocol errors.
var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already there")
)

// Invalid is a value the user gave that cannot be used. The text is Russian
// and goes to the user as is.
type Invalid struct{ Text string }

func (e *Invalid) Error() string { return e.Text }

func invalid(format string, a ...any) error { return &Invalid{Text: fmt.Sprintf(format, a...)} }

var hwidOK = regexp.MustCompile(`^[a-zA-Z0-9=-]{10,64}$`)

// ValidHWID reports whether s can serve as a device id.
func ValidHWID(s string) bool { return hwidOK.MatchString(s) }

func New() *Profile {
	return &Profile{Subs: []Sub{}, Servers: []Server{}, Favorites: []string{}, Mode: proto.ModeRule, NextSub: 1}
}

// Load reads profile.json; a missing file is a fresh profile.
func Load(path string) (*Profile, error) {
	p, err := store.LoadJSON(path, *New())
	if err != nil {
		return New(), err
	}
	if p.Mode != proto.ModeGlobal {
		p.Mode = proto.ModeRule
	}
	if p.NextSub < 1 {
		p.NextSub = 1
	}
	// The file is indented for a human; in memory the outbounds are compact,
	// so that SameServers can compare them to freshly parsed ones.
	for i := range p.Servers {
		p.Servers[i].Outbound = compact(p.Servers[i].Outbound)
		p.Servers[i].Xray = compact(p.Servers[i].Xray)
	}
	return &p, nil
}

func compact(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return raw
	}
	return buf.Bytes()
}

func (p *Profile) Save(path string) error { return store.SaveJSON(path, p) }

// Clone is a deep copy, for work that must not touch the loop's profile.
func (p *Profile) Clone() *Profile {
	raw, err := json.Marshal(p)
	if err != nil {
		panic(fmt.Sprintf("profile does not marshal: %v", err))
	}
	out := New()
	if err := json.Unmarshal(raw, out); err != nil {
		panic(fmt.Sprintf("profile does not unmarshal: %v", err))
	}
	return out
}

// EnsureHWID returns the id device-limited panels count this machine by,
// making one on first use. It must never change afterwards: a new id would
// take another device slot.
func (p *Profile) EnsureHWID() string {
	if !ValidHWID(p.HWID) {
		buf := make([]byte, 8)
		_, _ = rand.Read(buf)
		p.HWID = hex.EncodeToString(buf)
	}
	return p.HWID
}

func (p *Profile) Sub(id string) *Sub {
	for i := range p.Subs {
		if p.Subs[i].ID == id {
			return &p.Subs[i]
		}
	}
	return nil
}

func (p *Profile) Server(id string) *Server {
	for i := range p.Servers {
		if p.Servers[i].ID == id {
			return &p.Servers[i]
		}
	}
	return nil
}

// CountOf is how many servers a subscription has.
func (p *Profile) CountOf(subID string) int {
	n := 0
	for _, s := range p.Servers {
		if s.Sub == subID {
			n++
		}
	}
	return n
}

// CleanURL checks a subscription address and returns it trimmed.
func CleanURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", invalid("Это не похоже на ссылку подписки. Она начинается с https:// — скопируй её целиком у своего провайдера.")
	}
	return raw, nil
}

// AddSub registers a subscription; its servers arrive with ApplyFetch. An
// empty name becomes the host of the address until the panel says better.
func (p *Profile) AddSub(rawURL, name string) (*Sub, error) {
	name = cleanName(name)
	prefix := subs.CleanPrefix(name)
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return p.addSub(rawURL, name, prefix, false)
}

// AddLegacySub registers a subscription from the old subscription.txt with
// exactly the prefix the old tray used, so its servers keep their ids.
func (p *Profile) AddLegacySub(rawURL, prefix string) (*Sub, error) {
	return p.addSub(rawURL, prefix, prefix, true)
}

func (p *Profile) addSub(rawURL, name, prefix string, exactPrefix bool) (*Sub, error) {
	clean, err := CleanURL(rawURL)
	if err != nil {
		return nil, err
	}
	for _, s := range p.Subs {
		if s.URL == clean {
			return nil, ErrExists
		}
	}
	id := fmt.Sprintf("s%d", p.NextSub)
	p.NextSub++
	sub := Sub{ID: id, Name: name, Prefix: prefix, URL: clean}
	if sub.Name == "" {
		u, _ := url.Parse(clean)
		sub.Name, sub.AutoName = u.Hostname(), true
	}
	if !exactPrefix && (sub.Prefix == "" || p.prefixTaken(sub.Prefix)) {
		sub.Prefix = id
	}
	p.Subs = append(p.Subs, sub)
	return &p.Subs[len(p.Subs)-1], nil
}

func (p *Profile) prefixTaken(prefix string) bool {
	for _, s := range p.Subs {
		if s.Prefix == prefix {
			return true
		}
	}
	return false
}

func cleanName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > 60 {
		name = string(r[:60])
	}
	return name
}

func (p *Profile) RenameSub(id, name string) error {
	sub := p.Sub(id)
	if sub == nil {
		return ErrNotFound
	}
	name = cleanName(name)
	if name == "" {
		return invalid("Название не может быть пустым.")
	}
	sub.Name, sub.AutoName = name, false
	return nil
}

// RemoveSub deletes a subscription with its servers.
func (p *Profile) RemoveSub(id string) error {
	if p.Sub(id) == nil {
		return ErrNotFound
	}
	p.Subs = slices.DeleteFunc(p.Subs, func(s Sub) bool { return s.ID == id })
	p.Servers = slices.DeleteFunc(p.Servers, func(s Server) bool { return s.Sub == id })
	p.pruneFavorites()
	return nil
}

// ApplyFetch replaces a subscription's servers with what a download brought.
// A download without a single usable server changes nothing and is an error:
// one flaky provider must not empty the list.
//
// Ids stay stable between refreshes: links are sorted by name before they
// are tagged, and tags already used by other subscriptions are not taken.
func (p *Profile) ApplyFetch(id string, f subs.Fetched, useXray bool, now time.Time) (int, []subs.Skipped, error) {
	sub := p.Sub(id)
	if sub == nil {
		return 0, nil, ErrNotFound
	}
	used := subs.ReservedTags()
	for _, s := range p.Servers {
		if s.Sub != id {
			used[s.ID] = 1
		}
	}
	parsed, skipped := subs.ParseLinks(f.Links, sub.Prefix, used, useXray)
	if len(parsed) == 0 {
		err := errors.New("в подписке нет ни одного подходящего сервера")
		p.FetchFailed(id, err, now)
		return 0, skipped, err
	}

	fresh := make([]Server, len(parsed))
	for i, s := range parsed {
		fresh[i] = Server{Server: s, Sub: id}
	}
	// Put the new servers where the old ones stood, so the list keeps the
	// order of the subscriptions.
	at := slices.IndexFunc(p.Servers, func(s Server) bool { return s.Sub == id })
	rest := slices.DeleteFunc(slices.Clone(p.Servers), func(s Server) bool { return s.Sub == id })
	if at < 0 || at > len(rest) {
		at = p.insertPoint(rest, id)
	}
	p.Servers = slices.Insert(rest, at, fresh...)

	t := now.UTC().Truncate(time.Second)
	sub.UpdatedAt, sub.TriedAt, sub.Error = &t, &t, ""
	sub.ExpireAt, sub.UsedBytes, sub.TotalBytes = nil, nil, nil
	if f.Info != nil {
		sub.ExpireAt, sub.UsedBytes, sub.TotalBytes = f.Info.Expire, f.Info.Used, f.Info.Total
	}
	if sub.AutoName && f.Title != "" {
		sub.Name, sub.AutoName = cleanName(f.Title), false
	}
	return len(fresh), skipped, nil
}

// insertPoint finds where the servers of a subscription that has none yet
// belong: after the servers of the subscriptions listed before it.
func (p *Profile) insertPoint(servers []Server, id string) int {
	before := map[string]bool{}
	for _, s := range p.Subs {
		if s.ID == id {
			break
		}
		before[s.ID] = true
	}
	at := 0
	for i, s := range servers {
		if before[s.Sub] {
			at = i + 1
		}
	}
	return at
}

// FetchFailed records a failed download. The servers stay as they were.
func (p *Profile) FetchFailed(id string, err error, now time.Time) {
	if sub := p.Sub(id); sub != nil {
		t := now.UTC().Truncate(time.Second)
		sub.TriedAt, sub.Error = &t, err.Error()
	}
}

// AddLink adds a single server by its share link.
func (p *Profile) AddLink(link string, useXray bool) (*Server, error) {
	link = strings.TrimSpace(link)
	if !strings.Contains(link, "://") || strings.ContainsAny(link, " \n\t") {
		return nil, invalid("Это не похоже на ссылку сервера. Она начинается с vless://, trojan://, ss:// или hysteria2://.")
	}
	used := subs.ReservedTags()
	for _, s := range p.Servers {
		used[s.ID] = 1
	}
	parsed, skipped := subs.ParseLinks([]string{link}, "", used, useXray)
	if len(parsed) == 0 {
		reason := "ссылка не разобралась"
		if len(skipped) > 0 {
			reason = skipped[0].Reason
		}
		return nil, invalid("Сервер не добавлен: %s.", reason)
	}
	p.Servers = append(p.Servers, Server{Server: parsed[0]})
	return &p.Servers[len(p.Servers)-1], nil
}

// RemoveServer deletes a server added by link. A subscription's server
// would only come back on the next refresh, so those are refused.
func (p *Profile) RemoveServer(id string) error {
	s := p.Server(id)
	if s == nil {
		return ErrNotFound
	}
	if s.Sub != "" {
		return invalid("Этот сервер пришёл из подписки. Удалить можно только подписку целиком.")
	}
	p.Servers = slices.DeleteFunc(p.Servers, func(x Server) bool { return x.ID == id })
	p.pruneFavorites()
	return nil
}

func (p *Profile) Select(id string) error {
	if p.Server(id) == nil {
		return ErrNotFound
	}
	p.Selected = id
	return nil
}

// FixSelection makes sure the selected server exists: the first favourite
// that does, failing that the first server. It returns the id that was lost,
// or "" when the selection was fine or there was none to lose.
func (p *Profile) FixSelection() (lost string) {
	if p.Selected != "" && p.Server(p.Selected) != nil {
		return ""
	}
	lost, p.Selected = p.Selected, ""
	for _, id := range p.Favorites {
		if p.Server(id) != nil {
			p.Selected = id
			return lost
		}
	}
	if len(p.Servers) > 0 {
		p.Selected = p.Servers[0].ID
	}
	return lost
}

func (p *Profile) SetFavorite(id string, fav bool) error {
	if p.Server(id) == nil {
		return ErrNotFound
	}
	has := slices.Contains(p.Favorites, id)
	switch {
	case fav && !has:
		p.Favorites = append(p.Favorites, id)
	case !fav && has:
		p.Favorites = slices.DeleteFunc(p.Favorites, func(x string) bool { return x == id })
	}
	return nil
}

// Reorder puts the favourites in the given order. Favourites the list does
// not mention keep their relative order after the mentioned ones.
func (p *Profile) Reorder(ids []string) error {
	var out []string
	for _, id := range ids {
		if !slices.Contains(p.Favorites, id) {
			return ErrNotFound
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, id := range p.Favorites {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	p.Favorites = out
	return nil
}

// FavoriteOrder is a server's place among the favourites, -1 for others.
// Favourites whose server is gone for now do not take a place.
func (p *Profile) FavoriteOrder(id string) int {
	n := 0
	for _, f := range p.Favorites {
		if f == id {
			return n
		}
		if p.Server(f) != nil {
			n++
		}
	}
	return -1
}

// pruneFavorites forgets favourites whose server was removed for good. A
// server that merely dropped out of a refresh keeps its star: providers
// take servers down and bring them back.
func (p *Profile) pruneFavorites() {
	p.Favorites = slices.DeleteFunc(p.Favorites, func(id string) bool {
		return p.Server(id) == nil && !p.mayReturn(id)
	})
}

func (p *Profile) mayReturn(id string) bool {
	for _, s := range p.Subs {
		if s.Prefix != "" && strings.HasPrefix(id, s.Prefix+"-") {
			return true
		}
		if s.Prefix == "" {
			return true
		}
	}
	return false
}

// SameServers reports whether two profiles would build the same set of
// outbounds; if so the engine does not need a restart.
func SameServers(a, b []Server) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Via != b[i].Via ||
			string(a[i].Outbound) != string(b[i].Outbound) || string(a[i].Xray) != string(b[i].Xray) {
			return false
		}
	}
	return true
}

// retryAfter is how soon a failed refresh is tried again when the regular
// interval is longer.
const retryAfter = 30 * time.Minute

// Due reports whether any subscription is due for an automatic refresh.
// A subscription that has never been tried is due at once.
func (p *Profile) Due(now time.Time, interval time.Duration) bool {
	if interval <= 0 {
		return false
	}
	for _, s := range p.Subs {
		if s.TriedAt == nil {
			return true
		}
		wait := interval
		if s.Error != "" && wait > retryAfter {
			wait = retryAfter
		}
		if !now.Before(s.TriedAt.Add(wait)) {
			return true
		}
	}
	return false
}

// MaskURL hides the key of a subscription address: the host stays, the path
// shrinks to its last four characters. Clients never get the full address.
func MaskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "…"
	}
	rest := strings.TrimPrefix(raw, u.Scheme+"://"+u.Host)
	rest = strings.Trim(rest, "/")
	tail := ""
	if r := []rune(rest); len(r) > 8 {
		tail = string(r[len(r)-4:])
	}
	return u.Scheme + "://" + u.Host + "/…" + tail
}
