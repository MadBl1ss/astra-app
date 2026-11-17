package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"strelka/internal/geo"
	"strelka/internal/store"
	"strelka/proto"
)

// fakeGeo stands in for ip-api.com: no test goes to the internet.
type fakeGeo struct {
	srv  *httptest.Server
	hits atomic.Int32
	mu   sync.Mutex
	body string
	code int
}

func newFakeGeo(t *testing.T) *fakeGeo {
	f := &fakeGeo{code: 200, body: `{"status":"success","country":"Россия","countryCode":"RU","city":"Москва","lat":55.7558,"lon":37.6173}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.hits.Add(1)
		f.mu.Lock()
		code, body := f.code, f.body
		f.mu.Unlock()
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGeo) answer(code int, body string) {
	f.mu.Lock()
	f.code, f.body = code, body
	f.mu.Unlock()
}

func (f *fakeGeo) withLocator(o *Options) { o.Locator = geo.New(f.srv.URL + "/json/") }

func startPlaceRig(t *testing.T, paths store.Paths, f *fakeGeo) *rig {
	r := startRigWith(t, paths, "ok", f.withLocator)
	t.Cleanup(r.shutdown)
	return r
}

// First run: nothing known, so the daemon asks at once, keeps the answer
// and says so in the journal.
func TestPlaceIsLookedUpOnTheFirstRun(t *testing.T) {
	f := newFakeGeo(t)
	paths := newData(t)
	r := startPlaceRig(t, paths, f)
	r.waitState("место по IP", func(st proto.State) bool { return st.Place != nil })
	p := r.last.Place
	if p.City != "Москва" || p.Source != proto.PlaceIP || deref(p.Country) != "RU" || p.Lat != 55.7558 || p.At.IsZero() {
		t.Errorf("место: %+v", p)
	}
	// The answer may come before the test's client has subscribed: ask the
	// journal rather than wait for the notification.
	var jl proto.JournalListResult
	r.call(proto.MethodJournalList, proto.JournalListParams{}, &jl)
	found := false
	for _, e := range jl.Entries {
		found = found || (e.Level == proto.LevelInfo && strings.Contains(e.Text, "определено по IP"))
		if strings.Contains(e.Text, "Москва") {
			t.Errorf("город в журнале — а журнал уходит в отчёт: %q", e.Text)
		}
	}
	if !found {
		t.Errorf("в журнале нет записи о месте: %+v", jl.Entries)
	}
	saved, err := store.LoadJSON(paths.PlaceFile(), proto.Place{})
	if err != nil || saved.City != "Москва" {
		t.Errorf("place.json: %+v, %v", saved, err)
	}
	r.shutdown()

	// A fresh place is not asked again by the next run.
	before := f.hits.Load()
	r2 := startPlaceRig(t, paths, f)
	var st proto.State
	r2.call(proto.MethodStateGet, nil, &st)
	if st.Place == nil || st.Place.City != "Москва" {
		t.Errorf("место не пережило перезапуск: %+v", st.Place)
	}
	if f.hits.Load() != before {
		t.Error("свежее место спрашивать заново незачем")
	}
}

// A city chosen by hand is never overwritten by the automatic lookup, and
// place.detect is the way back to it.
func TestManualPlaceStandsUntilDetectIsAsked(t *testing.T) {
	f := newFakeGeo(t)
	paths := newData(t)
	r := startPlaceRig(t, paths, f)
	r.waitState("место по IP", func(st proto.State) bool { return st.Place != nil })

	ru := "ru"
	r.call(proto.MethodPlaceSet, proto.PlaceSetParams{Lat: 59.94, Lon: 30.31, City: " Санкт-Петербург ", Country: &ru}, nil)
	r.waitState("ручное место", func(st proto.State) bool { return st.Place != nil && st.Place.Source == proto.PlaceManual })
	if p := r.last.Place; p.City != "Санкт-Петербург" || deref(p.Country) != "RU" || p.Lat != 59.94 {
		t.Errorf("ручное место: %+v", p)
	}
	r.shutdown()

	before := f.hits.Load()
	r2 := startPlaceRig(t, paths, f)
	var st proto.State
	r2.call(proto.MethodStateGet, nil, &st)
	if st.Place == nil || st.Place.Source != proto.PlaceManual {
		t.Fatalf("ручное место не пережило перезапуск: %+v", st.Place)
	}
	if f.hits.Load() != before {
		t.Error("при ручном месте служба не должна спрашивать сервис сама")
	}

	f.answer(200, `{"status":"success","country":"Россия","countryCode":"RU","city":"Казань","lat":55.79,"lon":49.12}`)
	var got proto.Place
	r2.call(proto.MethodPlaceDetect, nil, &got)
	if got.City != "Казань" || got.Source != proto.PlaceIP {
		t.Errorf("place.detect ответил %+v", got)
	}
	r2.waitState("снова по IP", func(st proto.State) bool { return st.Place != nil && st.Place.Source == proto.PlaceIP })
}

func TestPlaceDetectFailureKeepsThePlace(t *testing.T) {
	f := newFakeGeo(t)
	r := startPlaceRig(t, newData(t), f)
	r.waitState("место по IP", func(st proto.State) bool { return st.Place != nil })

	f.answer(200, `{"status":"fail","message":"reserved range"}`)
	err := r.tryCall(proto.MethodPlaceDetect, nil, nil)
	var perr *proto.Error
	if !errors.As(err, &perr) || perr.Code != proto.CodeFetchFailed {
		t.Fatalf("ждали 1005, получили %v", err)
	}
	var st proto.State
	r.call(proto.MethodStateGet, nil, &st)
	if st.Place == nil || st.Place.City != "Москва" {
		t.Errorf("неудача не должна стирать место: %+v", st.Place)
	}
}

func TestPlaceWithoutLocator(t *testing.T) {
	paths := newData(t)
	r := startRig(t, paths, "ok")
	defer r.shutdown()
	var st proto.State
	r.call(proto.MethodStateGet, nil, &st)
	if st.Place != nil {
		t.Errorf("без геосервиса места нет, а в State %+v", st.Place)
	}
	err := r.tryCall(proto.MethodPlaceDetect, nil, nil)
	var perr *proto.Error
	if !errors.As(err, &perr) || perr.Code != proto.CodeUnsupported {
		t.Errorf("ждали 1007, получили %v", err)
	}
	// The city list still works: it needs no service.
	r.call(proto.MethodPlaceSet, proto.PlaceSetParams{Lat: 55.75, Lon: 37.62, City: "Москва"}, nil)
	if _, err := os.Stat(paths.PlaceFile()); err != nil {
		t.Errorf("place.json: %v", err)
	}
}

func TestPlaceSetChecksItsParams(t *testing.T) {
	r := startRig(t, newData(t), "ok")
	defer r.shutdown()
	bad := "RUS"
	for name, p := range map[string]proto.PlaceSetParams{
		"latitude":  {Lat: 91, Lon: 0, City: "X"},
		"longitude": {Lat: 0, Lon: -181, City: "X"},
		"no city":   {Lat: 10, Lon: 10, City: "   "},
		"country":   {Lat: 10, Lon: 10, City: "X", Country: &bad},
	} {
		err := r.tryCall(proto.MethodPlaceSet, p, nil)
		var perr *proto.Error
		if !errors.As(err, &perr) || perr.Code != proto.CodeInvalidParams {
			t.Errorf("%s: ждали -32602, получили %v", name, err)
		}
	}
}
