package subs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// subPage is a subscription page the way providers make them: the address
// in a script, buttons for each app, some of them completed by the script,
// one for Clash with an address of its own. Every address is made up.
func subPage(sub string) string {
	return `<!doctype html><html lang="ru"><head><title>VPN — подписка</title>
<script src="https://cdn.example.com/app.js"></script></head><body>
<a href="https://t.me/example_bot">Бот</a>
<a href="https://apps.apple.com/app/id000">Happ в App Store</a>
<a class="btn" href="hiddify://import/` + sub + `#Мой VPN">Hiddify</a>
<a class="btn" href="clash://install-config?url=` + url.QueryEscape(sub+"/clash") + `&amp;name=VPN">Clash</a>
<script>
  const subscriptionUrl = "` + sub + `";
  const apps = { happ: 'happ://add/' + subscriptionUrl, incy: "incy://add/" + subscriptionUrl };
  const v2rayNgImport = "v2rayng://install-sub?url=` + url.QueryEscape(sub) + `#VPN";
  const escaped = "streisand:\/\/import\/` + strings.ReplaceAll(sub, "/", `\/`) + `";
</script></body></html>`
}

func TestSubFromPageTakesWhatTheButtonsAgreeOn(t *testing.T) {
	sub := "https://sub.example.com/abc123/def456"
	if got := subFromPage(subPage(sub)); got != sub {
		t.Errorf("со страницы: %q, а нужно %q", got, sub)
	}
	for what, page := range map[string]string{
		"no buttons":            `<html><a href="https://t.me/example_bot">Бот</a> https://sub.example.com/abc</html>`,
		"only script-made ones": `<script>const a = 'happ://add/' + url; const b = "hiddify://import/" + url;</script>`,
		"only encrypted":        `<a href="happ://crypt5/QUJDREVGR0hJSktMTU5PUA">Happ</a>`,
		"not a page at all":     "",
	} {
		if got := subFromPage(page); got != "" {
			t.Errorf("%s: нашлось %q", what, got)
		}
	}
}

// A short link in front of a page: apps get 404, a browser gets the page.
func TestFetchFindsTheSubscriptionBehindAShortLink(t *testing.T) {
	var pageHWID []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		browser := r.Header.Get("Sec-Fetch-Mode") == "navigate"
		switch r.URL.Path {
		case "/s/abc", "/s/empty":
			if !browser {
				w.WriteHeader(http.StatusNotFound) // what short links give apps
				return
			}
			pageHWID = append(pageHWID, r.Header.Get("x-hwid"))
			if r.URL.Path == "/s/empty" {
				fmt.Fprint(w, `<html><body>Ссылка устарела</body></html>`)
				return
			}
			fmt.Fprint(w, subPage(srv.URL+"/sub/real-key"))
		case "/s/nowhere":
			if !browser {
				w.WriteHeader(http.StatusFound) // a redirect without an address
				return
			}
			fmt.Fprint(w, subPage(srv.URL+"/sub/real-key"))
		case "/landing":
			fmt.Fprint(w, subPage(srv.URL+"/sub/real-key")) // a page for everyone
		case "/s/full":
			// The page is there, its panel turns this device away.
			fmt.Fprint(w, subPage(srv.URL+"/sub/no-room"))
		case "/sub/no-room":
			w.WriteHeader(http.StatusNotFound)
		case "/plain-page":
			fmt.Fprint(w, `<!doctype html><html><body>Здесь ничего нет</body></html>`)
		case "/sub/real-key":
			if r.Header.Get("x-hwid") == "" || browser {
				http.Error(w, "no device", http.StatusForbidden)
				return
			}
			fmt.Fprint(w, "vless://id@de.example.com:443?security=tls#DE\ntrojan://pw@nl.example.com:443#NL\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	for _, path := range []string{"/s/abc", "/s/nowhere", "/landing"} {
		got, err := Fetch(ctx, srv.Client(), srv.URL+path, "abcdef0123456789")
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got.MovedTo != srv.URL+"/sub/real-key" || len(got.Links) != 2 {
			t.Errorf("%s: %+v", path, got)
		}
	}
	for _, h := range pageHWID {
		if h != "" {
			t.Errorf("страница получила x-hwid: его считает панель, а не страница")
		}
	}

	// A plain 404 stays a 404: most of them are mistyped links.
	if _, err := Fetch(ctx, srv.Client(), srv.URL+"/s/empty", "abcdef0123456789"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("короткая ссылка на пустую страницу: %v", err)
	}
	if _, err := Fetch(ctx, srv.Client(), srv.URL+"/missing", "abcdef0123456789"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("несуществующий адрес: %v", err)
	}
	// The page led somewhere real and the panel said 404: most likely its
	// device limit is taken, which some panels hide behind a 404.
	if _, err := Fetch(ctx, srv.Client(), srv.URL+"/s/full", "abcdef0123456789"); err == nil ||
		!strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "лимит устройств") {
		t.Errorf("панель без места для устройства: %v", err)
	}
	// A page with nothing on it says it is a page.
	if _, err := Fetch(ctx, srv.Client(), srv.URL+"/plain-page", "abcdef0123456789"); err == nil || !strings.Contains(err.Error(), "страница") {
		t.Errorf("страница без кнопок: %v", err)
	}
}
