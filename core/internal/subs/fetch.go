package subs

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// FetchTimeout bounds one subscription download.
const FetchTimeout = 25 * time.Second

// Fetched is what one subscription download brought.
type Fetched struct {
	Links []string
	// Title is the panel's name for the subscription (header profile-title),
	// "" when it sends none.
	Title string
	// Info is the traffic and expiry the panel reports, nil when it does not.
	Info *UserInfo
}

// UserInfo is the subscription-userinfo header. A nil field means the panel
// did not say, or said "unlimited".
type UserInfo struct {
	Used   *int64
	Total  *int64
	Expire *time.Time
}

// deviceOS is what x-device-os carries.
func deviceOS() string {
	if runtime.GOOS == "windows" {
		return "Windows"
	}
	return "Linux"
}

// Fetch downloads one subscription and returns its share links.
//
// Remnawave panels with a device limit answer a request without x-hwid with
// a stub server named "App not supported", so every request carries this
// machine's id. The same headers are what Happ or v2RayTun would send.
//
// Errors are Russian texts: they end up in the subscription's "error" field.
func Fetch(ctx context.Context, hc *http.Client, subURL, hwid string) (Fetched, error) {
	var out Fetched
	if hc == nil {
		hc = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", subURL, nil)
	if err != nil {
		return out, errors.New("ссылка подписки записана неверно")
	}
	req.Header.Set("User-Agent", "sing-box")
	req.Header.Set("x-hwid", hwid)
	req.Header.Set("x-device-os", deviceOS())
	// The old tray's value, kept on purpose: the panel has this device on
	// record under it, and a renamed device is one risk too many for a slot
	// on a device-limited plan.
	req.Header.Set("x-device-model", "sing-box-tray")
	resp, err := hc.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return out, errors.New("сервер подписки не ответил вовремя")
		}
		return out, errors.New("не удалось связаться с сервером подписки")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("сервер подписки ответил ошибкой %d", resp.StatusCode)
	}
	if resp.Header.Get("x-hwid-max-devices-reached") == "true" {
		return out, errors.New("лимит устройств исчерпан — удали старое устройство в боте провайдера")
	}
	if resp.Header.Get("x-hwid-not-supported") == "true" {
		return out, errors.New("провайдер не принял это устройство")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return out, errors.New("ответ сервера подписки оборвался")
	}

	out.Links = SplitLinks(string(body))
	out.Title = decodeTitle(resp.Header.Get("profile-title"))
	out.Info = ParseUserInfo(resp.Header.Get("subscription-userinfo"))
	return out, nil
}

// SplitLinks reads the body of a subscription: a list of share links, one
// per line. Most providers base64 the list; some serve it as plain text.
func SplitLinks(body string) []string {
	text := strings.TrimSpace(body)
	if dec, err := base64.StdEncoding.DecodeString(text); err == nil {
		text = string(dec)
	} else if dec, err := base64.RawStdEncoding.DecodeString(text); err == nil {
		text = string(dec)
	}
	var links []string
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.Contains(ln, "://") {
			links = append(links, ln)
		}
	}
	return links
}

// decodeTitle reads profile-title, which panels send either plain or as
// "base64:<utf-8 text>" because headers cannot carry Cyrillic.
func decodeTitle(h string) string {
	h = strings.TrimSpace(h)
	if rest, ok := strings.CutPrefix(h, "base64:"); ok {
		if dec, err := decodeB64(rest); err == nil {
			h = strings.TrimSpace(dec)
		}
	}
	if r := []rune(h); len(r) > 60 {
		h = string(r[:60])
	}
	return h
}

// ParseUserInfo reads "upload=1; download=2; total=3; expire=1767225600".
// Zero total and zero expire mean "no limit" and come out as nil.
func ParseUserInfo(h string) *UserInfo {
	if strings.TrimSpace(h) == "" {
		return nil
	}
	vals := map[string]int64{}
	for _, part := range strings.Split(h, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n >= 0 {
			vals[strings.ToLower(strings.TrimSpace(k))] = n
		}
	}
	if len(vals) == 0 {
		return nil
	}
	info := &UserInfo{}
	_, hasUp := vals["upload"]
	_, hasDown := vals["download"]
	if hasUp || hasDown {
		used := vals["upload"] + vals["download"]
		info.Used = &used
	}
	if t := vals["total"]; t > 0 {
		info.Total = &t
	}
	if e := vals["expire"]; e > 0 {
		at := time.Unix(e, 0).UTC()
		info.Expire = &at
	}
	return info
}

// ListEntry is one line of the old Strelka's subscription.txt.
type ListEntry struct {
	Name string // tag prefix, "" only for a lone unnamed subscription
	URL  string
}

var validName = regexp.MustCompile(`[^A-Za-z0-9_.]`)

// CleanPrefix reduces a name to what may prefix a server tag.
func CleanPrefix(name string) string { return validName.ReplaceAllString(name, "") }

// ParseList reads the old subscription.txt: one subscription per line,
// optionally preceded by a short name that prefixes its servers' tags.
// Blank lines and # comments are skipped, and so is any line without a
// "://" link: a stray word or a BOM (the three bytes Notepad prepends) glued to the first comment must not pass
// for a subscription.
func ParseList(s string) []ListEntry {
	var subs []ListEntry
	s = strings.TrimPrefix(s, string(rune(0xFEFF))) // the BOM Notepad prepends
	for _, ln := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		f := strings.Fields(ln)
		sub := ListEntry{URL: f[len(f)-1]}
		if !strings.Contains(sub.URL, "://") {
			continue
		}
		if len(f) > 1 {
			sub.Name = CleanPrefix(f[0])
		}
		subs = append(subs, sub)
	}
	// A lone unnamed subscription keeps plain tags, as it always has.
	if len(subs) > 1 {
		seen := map[string]bool{}
		for i := range subs {
			if subs[i].Name == "" || seen[subs[i].Name] {
				subs[i].Name = fmt.Sprintf("sub%d", i+1)
			}
			seen[subs[i].Name] = true
		}
	}
	return subs
}
