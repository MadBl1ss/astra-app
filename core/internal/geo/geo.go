// Package geo finds roughly where the computer is, from its public address.
//
// It exists for one picture: the globe on the window's Home draws a beam
// from here to the server's country. One free service is asked, ip-api.com:
// it needs no key, answers in Russian and gives the coordinates right away.
// Its free endpoint is plain HTTP and for non-commercial use only; a build
// that is sold would point the Locator at a paid one.
package geo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultURL asks for the fields the window needs and nothing else.
const DefaultURL = "http://ip-api.com/json/?fields=status,message,country,countryCode,city,lat,lon&lang=ru"

// Timeout bounds one lookup end to end.
const Timeout = 10 * time.Second

// Place is where the service believes the address to be.
type Place struct {
	Lat, Lon float64
	// City is what to show: the city, or the country when the service does
	// not know the city. May be empty.
	City string
	// Country is the ISO code in upper case, or empty.
	Country string
}

// Why a lookup failed, as far as the user is concerned.
var (
	// ErrUnreachable: no answer at all - no internet, the service is down
	// or blocked.
	ErrUnreachable = errors.New("geo: service unreachable")
	// ErrRefused: the service answered, but not with a place.
	ErrRefused = errors.New("geo: service gave no place")
)

// Locator asks one service. The zero value is not usable; see New.
type Locator struct {
	url    string
	client *http.Client
}

// New makes a locator for a service URL that answers like ip-api.com.
func New(serviceURL string) *Locator {
	return &Locator{
		url: serviceURL,
		client: &http.Client{
			Timeout: Timeout,
			// Never through a proxy from the environment: the point is the
			// address the computer itself goes out with.
			Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext},
		},
	}
}

// Host is the name the service is asked by, for the engine's rule that
// sends the daemon's request past the VPN. Empty when the URL names an
// address rather than a host: a rule by domain would never match it.
func (l *Locator) Host() string {
	u, err := url.Parse(l.url)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return ""
	}
	return host
}

// answer is ip-api.com's reply; message comes only with status "fail".
type answer struct {
	Status      string  `json:"status"`
	Message     string  `json:"message"`
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	City        string  `json:"city"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
}

// Locate asks the service where this computer is.
func (l *Locator) Locate(ctx context.Context) (Place, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.url, nil)
	if err != nil {
		return Place{}, err
	}
	// A plain name: nothing about the user or the machine goes along.
	req.Header.Set("User-Agent", "strelka")
	resp, err := l.client.Do(req)
	if err != nil {
		return Place{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 429 is the free endpoint's limit of 45 a minute; the daemon asks
		// once a day, so it means someone else on the same address.
		return Place{}, fmt.Errorf("%w: HTTP %d", ErrRefused, resp.StatusCode)
	}
	var a answer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&a); err != nil {
		return Place{}, fmt.Errorf("%w: %v", ErrRefused, err)
	}
	if a.Status != "success" {
		return Place{}, fmt.Errorf("%w: %s %q", ErrRefused, a.Status, a.Message)
	}
	// Exactly 0, 0 is in the ocean off Africa: a reply with the numbers
	// missing, not a place.
	if a.Lat < -90 || a.Lat > 90 || a.Lon < -180 || a.Lon > 180 || (a.Lat == 0 && a.Lon == 0) {
		return Place{}, fmt.Errorf("%w: coordinates %v, %v", ErrRefused, a.Lat, a.Lon)
	}
	p := Place{Lat: a.Lat, Lon: a.Lon, City: strings.TrimSpace(a.City)}
	if p.City == "" {
		p.City = strings.TrimSpace(a.Country)
	}
	if code := strings.ToUpper(strings.TrimSpace(a.CountryCode)); len(code) == 2 {
		p.Country = code
	}
	return p, nil
}
