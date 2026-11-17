package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"strelka/internal/confgen"
	"strelka/internal/geo"
	"strelka/internal/store"
	"strelka/proto"
)

// Where the computer is, for the globe on the window's Home. The window
// only draws it; the daemon finds it out, because the window may not go to
// the internet at all (its CSP allows nothing but the channel), and because
// only the daemon can make the request past the VPN.

const (
	// placeRefresh is how old a place found by IP may get: a laptop that
	// moved shows the new city by the next day.
	placeRefresh = 24 * time.Hour
	// placeRetry is the pause after an automatic lookup that failed.
	placeRetry = 10 * time.Minute
)

// placeState belongs to the loop.
type placeState struct {
	locating bool            // a lookup is on its way
	tried    time.Time       // the last automatic attempt
	waiters  []chan rpcReply // place.detect calls waiting for it
}

func (d *Daemon) loadPlace() {
	var saved *proto.Place
	saved, err := store.LoadJSON(d.opts.Paths.PlaceFile(), saved)
	if err != nil {
		// Only a picture depends on it: start without, it will be asked again.
		d.opts.Logf("place unreadable, will look it up again: %v", err)
		return
	}
	if saved != nil && validPlace(saved.Lat, saved.Lon) {
		d.st.Place = saved
	}
}

func (d *Daemon) setPlace(p proto.Place) {
	d.st.Place = &p
	if err := store.SaveJSON(d.opts.Paths.PlaceFile(), p); err != nil {
		d.opts.Logf("saving the place: %v", err)
	}
	d.touch()
}

// selfRule is what the engine's config needs to send the daemon's lookup
// past the VPN: the daemon's own path and the service's name.
func (d *Daemon) selfRule() (match string, hosts []string) {
	if d.opts.Locator == nil {
		return "", nil
	}
	host := d.opts.Locator.Host()
	self, err := os.Executable()
	if host == "" || err != nil {
		return "", nil
	}
	return confgen.ExactPath(self), []string{host}
}

// autoPlace looks the place up when it is unknown or a day old, unless the
// user chose it by hand. Called at the start and once a minute.
func (d *Daemon) autoPlace(now time.Time) {
	if d.opts.Locator == nil || d.ps.locating {
		return
	}
	if p := d.st.Place; p != nil && (p.Source == proto.PlaceManual || now.Sub(p.At) < placeRefresh) {
		return
	}
	if !d.ps.tried.IsZero() && now.Sub(d.ps.tried) < placeRetry {
		return
	}
	d.ps.tried = now
	d.locate()
}

// locate starts a lookup off the loop; its answer comes back as a loopFunc.
func (d *Daemon) locate() {
	d.ps.locating = true
	loc := d.opts.Locator
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), geo.Timeout)
		defer cancel()
		p, err := loc.Locate(ctx)
		d.post(func() { d.located(p, err) })
	}()
}

func (d *Daemon) located(p geo.Place, err error) {
	d.ps.locating = false
	waiters := d.ps.waiters
	d.ps.waiters = nil
	if err != nil {
		d.opts.Logf("looking the place up by IP: %v", err)
		why := "Сервис не ответил. Проверь, есть ли интернет, и попробуй позже."
		if errors.Is(err, geo.ErrRefused) {
			why = "Сервис ответил, но места не назвал. Попробуй позже или выбери город вручную."
		}
		for _, w := range waiters {
			w <- rpcReply{err: proto.Errorf(proto.CodeFetchFailed, "Не удалось определить место по IP. %s", why)}
		}
		return
	}
	// The user chose a city while the automatic lookup was on its way: the
	// choice stands. An explicit place.detect is the user asking for this.
	if len(waiters) == 0 && d.st.Place != nil && d.st.Place.Source == proto.PlaceManual {
		return
	}
	prev := d.st.Place
	next := proto.Place{
		Lat:    p.Lat,
		Lon:    p.Lon,
		City:   p.City,
		Source: proto.PlaceIP,
		At:     time.Now().UTC().Truncate(time.Second),
	}
	if p.Country != "" {
		code := p.Country
		next.Country = &code
	}
	d.setPlace(next)
	// The journal says that it happened, not where: the journal goes into
	// the report for support, and the city has no business there.
	if prev == nil || prev.Source != next.Source || prev.City != next.City {
		d.log(proto.CatSystem, proto.LevelInfo, "Место на глобусе определено по IP.")
	}
	for _, w := range waiters {
		w <- rpcReply{result: next}
	}
}

// placeDetect is place.detect: look it up now and go back to automatic.
func (d *Daemon) placeDetect(c rpcCall) (any, *proto.Error, bool) {
	if d.opts.Locator == nil {
		return nil, proto.Errorf(proto.CodeUnsupported, "Эта служба запущена без определения места по IP. Выбери город вручную."), false
	}
	d.ps.waiters = append(d.ps.waiters, c.reply)
	if !d.ps.locating {
		d.locate()
	}
	return nil, nil, true
}

func (d *Daemon) placeSet(c rpcCall) (any, *proto.Error) {
	var p proto.PlaceSetParams
	if err := json.Unmarshal(c.params, &p); err != nil {
		return nil, proto.Errorf(proto.CodeInvalidParams, "Неверные параметры: нужны lat, lon и city.")
	}
	city := strings.TrimSpace(p.City)
	if !validPlace(p.Lat, p.Lon) {
		return nil, proto.Errorf(proto.CodeInvalidParams, "Неверные координаты: широта от −90 до 90, долгота от −180 до 180.")
	}
	if city == "" || utf8.RuneCountInString(city) > 80 {
		return nil, proto.Errorf(proto.CodeInvalidParams, "Нужно название города, не длиннее 80 символов.")
	}
	next := proto.Place{
		Lat:    p.Lat,
		Lon:    p.Lon,
		City:   city,
		Source: proto.PlaceManual,
		At:     time.Now().UTC().Truncate(time.Second),
	}
	if p.Country != nil {
		code := strings.ToUpper(strings.TrimSpace(*p.Country))
		if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
			return nil, proto.Errorf(proto.CodeInvalidParams, "Код страны — две латинские буквы.")
		}
		next.Country = &code
	}
	d.setPlace(next)
	d.log(proto.CatSystem, proto.LevelInfo, "Место на глобусе выбрано вручную.")
	return nil, nil
}

func validPlace(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lon) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}
