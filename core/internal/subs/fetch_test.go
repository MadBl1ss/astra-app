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
