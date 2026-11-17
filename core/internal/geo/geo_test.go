package geo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// serve stands in for ip-api.com; no test here goes to the internet.
func serve(t *testing.T, status int, body string) *Locator {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != "strelka" {
			t.Errorf("User-Agent = %q, ждали strelka", ua)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL + "/json/?fields=status,message,country,countryCode,city,lat,lon&lang=ru")
}

func TestLocateReadsTheAnswer(t *testing.T) {
	l := serve(t, 200, `{"status":"success","country":"Россия","countryCode":"ru","city":"Москва","lat":55.7558,"lon":37.6173}`)
	p, err := l.Locate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.City != "Москва" || p.Country != "RU" || p.Lat != 55.7558 || p.Lon != 37.6173 {
		t.Errorf("место разобрано неверно: %+v", p)
	}
}

func TestLocateFallsBackToTheCountryName(t *testing.T) {
	l := serve(t, 200, `{"status":"success","country":"Финляндия","countryCode":"FI","city":"","lat":60.17,"lon":24.94}`)
	p, err := l.Locate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.City != "Финляндия" {
		t.Errorf("без города показываем страну, а получили %q", p.City)
	}
}

func TestLocateRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
	}{
		"fail":         {200, `{"status":"fail","message":"private range"}`},
		"limit":        {429, ``},
		"garbage":      {200, `<html>captive portal</html>`},
		"no numbers":   {200, `{"status":"success","city":"Москва"}`},
		"out of range": {200, `{"status":"success","city":"X","lat":91,"lon":10}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := serve(t, c.status, c.body).Locate(context.Background())
			if !errors.Is(err, ErrRefused) {
				t.Errorf("ждали ErrRefused, получили %v", err)
			}
		})
	}
}

func TestLocateUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close() // nobody listens there any more
	_, err := New(addr).Locate(context.Background())
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("ждали ErrUnreachable, получили %v", err)
	}
}

func TestLocateGivesUpWithTheContext(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	t.Cleanup(func() { close(block); srv.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := New(srv.URL).Locate(ctx)
	if !errors.Is(err, ErrUnreachable) || time.Since(start) > 5*time.Second {
		t.Errorf("молчащий сервис должен отпускать по контексту: %v за %v", err, time.Since(start))
	}
}

func TestHost(t *testing.T) {
	if h := New(DefaultURL).Host(); h != "ip-api.com" {
		t.Errorf("Host = %q", h)
	}
	if h := New("http://127.0.0.1:8080/json").Host(); h != "" {
		t.Errorf("для адреса правило по имени бессмысленно, а Host = %q", h)
	}
	if !strings.HasPrefix(DefaultURL, "http://ip-api.com/") {
		t.Error("бесплатный ip-api отвечает только по http")
	}
}
