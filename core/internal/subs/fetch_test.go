package subs

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchSendsDeviceHeadersAndReadsMeta(t *testing.T) {
	links := "vless://id@a.example.com:443?security=tls#A\ntrojan://p@b.example.com:443#B\n"
	var seen http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.Header().Set("profile-title", "base64:"+base64.StdEncoding.EncodeToString([]byte("Мой провайдер")))
		w.Header().Set("subscription-userinfo", "upload=100; download=900; total=5000; expire=1767225600")
		fmt.Fprint(w, base64.StdEncoding.EncodeToString([]byte(links)))
	}))
	defer srv.Close()

	got, err := Fetch(context.Background(), srv.Client(), srv.URL, "abcdef0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if seen.Get("x-hwid") != "abcdef0123456789" || seen.Get("User-Agent") != "sing-box" || seen.Get("x-device-os") == "" {
		t.Errorf("заголовки запроса: %v", seen)
	}
	if len(got.Links) != 2 || !strings.HasPrefix(got.Links[1], "trojan://") {
		t.Errorf("ссылки: %v", got.Links)
	}
	if got.Title != "Мой провайдер" {
		t.Errorf("название: %q", got.Title)
	}
	if got.Info == nil || *got.Info.Used != 1000 || *got.Info.Total != 5000 ||
		!got.Info.Expire.Equal(time.Unix(1767225600, 0)) {
		t.Errorf("трафик и срок: %+v", got.Info)
	}
}

func TestFetchErrorsAreWordedForTheUser(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"ошибкой 404": func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
		"лимит устройств": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("x-hwid-max-devices-reached", "true")
		},
		"не принял это устройство": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("x-hwid-not-supported", "true")
		},
	}
	for want, h := range cases {
		srv := httptest.NewServer(h)
		_, err := Fetch(context.Background(), srv.Client(), srv.URL, "x")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ожидал ошибку со словами %q, получил %v", want, err)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // nobody listens any more
	if _, err := Fetch(context.Background(), nil, srv.URL, "x"); err == nil || !strings.Contains(err.Error(), "не удалось связаться") {
		t.Errorf("недоступный сервер: %v", err)
	}
}

func TestSplitLinksPlainAndBase64(t *testing.T) {
	plain := "# comment\nvless://id@a.example.com:443#A\n\nss://x@b.example.com:1#B\n"
	for name, body := range map[string]string{
		"текст":               plain,
		"base64":              base64.StdEncoding.EncodeToString([]byte(plain)),
		"base64 без паддинга": base64.RawStdEncoding.EncodeToString([]byte(plain)),
	} {
		if got := SplitLinks(body); len(got) != 2 {
			t.Errorf("%s: ссылок %d, ожидал 2: %v", name, len(got), got)
		}
	}
}

func TestParseUserInfo(t *testing.T) {
	if ParseUserInfo("") != nil || ParseUserInfo("garbage") != nil {
		t.Error("пустой и битый заголовок должны давать nil")
	}
	// Zero total and zero expire are how panels say "no limit".
	info := ParseUserInfo("upload=5; download=7; total=0; expire=0")
	if info == nil || *info.Used != 12 || info.Total != nil || info.Expire != nil {
		t.Errorf("безлимит: %+v", info)
	}
}

// A sing-box config as Remnawave serves it to "sing-box": its DNS and
// rule-set addresses must not be taken for servers.
const singBoxConfig = `{
  "dns": {"servers": [{"tag": "dns-remote", "address": "https://1.1.1.1/dns-query"}]},
  "outbounds": [{"type": "vless", "tag": "DE", "server": "de.example.com", "server_port": 443}],
  "route": {"rule_set": [{"tag": "geoip-ru", "url": "https://rules.example.com/geoip-ru.srs"}]}
}`

func TestFetchAsksAgainForTheListWhenAConfigCame(t *testing.T) {
	var agents, hwids []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.UserAgent())
		hwids = append(hwids, r.Header.Get("x-hwid"))
		if r.UserAgent() == "sing-box" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, singBoxConfig)
			return
		}
		w.Header().Set("profile-title", "Провайдер")
		fmt.Fprint(w, base64.StdEncoding.EncodeToString([]byte("vless://id@de.example.com:443?security=tls#DE\n")))
	}))
	defer srv.Close()

	got, err := Fetch(context.Background(), srv.Client(), srv.URL, "abcdef0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Links) != 1 || !strings.HasPrefix(got.Links[0], "vless://") || got.Title != "Провайдер" {
		t.Errorf("второй ответ не прочитан: %+v", got)
	}
	if len(agents) != 2 || agents[0] != "sing-box" || agents[1] != listAgent {
		t.Errorf("User-Agent запросов: %v", agents)
	}
	if hwids[1] != "abcdef0123456789" {
		t.Errorf("повтор без x-hwid: панель увидела бы новое устройство")
	}
	if got.MovedTo != "" {
		t.Errorf("адрес не менялся, а MovedTo = %q", got.MovedTo)
	}
}

func TestFetchGivesUpOnConfigsWithAClearReason(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		fmt.Fprint(w, singBoxConfig)
	}))
	defer srv.Close()
	_, err := Fetch(context.Background(), srv.Client(), srv.URL, "abcdef0123456789")
	if err == nil || !strings.Contains(err.Error(), "готовые настройки") {
		t.Errorf("ошибка: %v", err)
	}
	if n != 2 {
		t.Errorf("запросов %d, а нужно два: обычный и как v2rayN", n)
	}
}

func TestFetchFollowsARedirectToAnAppLink(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/r":
			http.Redirect(w, r, "happ://add/"+srv.URL+"/list#Мой", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "happ://add/"+srv.URL+"/loop", http.StatusFound)
		case "/crypt":
			http.Redirect(w, r, "happ://crypt5/QUJDREVG", http.StatusFound)
		case "/hop":
			http.Redirect(w, r, "/r", http.StatusFound) // an ordinary redirect first
		case "/list":
			if r.Header.Get("x-hwid") == "" {
				http.Error(w, "no hwid", http.StatusForbidden)
				return
			}
			fmt.Fprint(w, "trojan://p@de.example.com:443#DE\n")
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	for _, path := range []string{"/r", "/hop"} {
		got, err := Fetch(ctx, srv.Client(), srv.URL+path, "abcdef0123456789")
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got.MovedTo != srv.URL+"/list" || len(got.Links) != 1 {
			t.Errorf("%s: %+v", path, got)
		}
	}
	if _, err := Fetch(ctx, srv.Client(), srv.URL+"/crypt", "abcdef0123456789"); err == nil ||
		!strings.Contains(err.Error(), "зашифрованной для приложения Happ") {
		t.Errorf("зашифрованная ссылка: %v", err)
	}
	if _, err := Fetch(ctx, srv.Client(), srv.URL+"/loop", "abcdef0123456789"); err == nil ||
		!strings.Contains(err.Error(), "отправил в приложение") {
		t.Errorf("переадресация по кругу: %v", err)
	}
}
