// Package speed measures how fast a server downloads: a file fetched through
// the engine's gate (see confgen.Gate) for a few seconds or a few megabytes,
// whichever comes first. The first moments are left out of the number: a
// connection starts slow and speeds up, and a speed test is about the speed
// it settles at, not the handshake.
package speed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// URLs are what a speed test downloads, in order of preference: big files
// on networks that are fast everywhere. The next one is tried when one does
// not answer at all.
//
// Cloudflare answers 403 to a request of 100 000 000 bytes or more (checked
// 23.11.2026); 50 MB is still well past Default.Bytes.
var URLs = []string{
	"https://speed.cloudflare.com/__down?bytes=50000000",
	"https://nbg1-speed.hetzner.com/100MB.bin",
}

// Limits bound one download. Time counts from the first byte.
type Limits struct {
	Bytes int64
	Time  time.Duration
}

// Default is what a speed test spends on one server: a few seconds are
// enough to see where the speed settles, and 25 MB is about what a minute
// of HD video costs.
var Default = Limits{Bytes: 25 << 20, Time: 8 * time.Second}

// Sample is the download under way.
type Sample struct {
	Bytes   int64
	Elapsed time.Duration // since the first byte
	Mbps    float64       // over the last moments, for a needle that moves
}

// Result is a finished download.
type Result struct {
	Bytes   int64
	Elapsed time.Duration // from the first byte to the end
	TTFB    time.Duration // from the request to the first byte
	Mbps    float64       // the settled speed
}

// Mbps turns bytes over a span into megabits per second.
func Mbps(bytes int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(bytes) * 8 / 1e6 / d.Seconds()
}

// Open starts a download of u through the gate at addr as user. The answer's
// body is the caller's to close; ctx bounds all of it.
func Open(ctx context.Context, addr, user, password, u string) (io.ReadCloser, error) {
	proxy := &url.URL{Scheme: "http", Host: addr, User: url.UserPassword(user, password)}
	tr := &http.Transport{
		Proxy:                 http.ProxyURL(proxy),
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		// A compressed answer would measure the compression, not the server.
		DisableCompression: true,
		DisableKeepAlives:  true,
		ForceAttemptHTTP2:  false,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		tr.CloseIdleConnections()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		tr.CloseIdleConnections()
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	return resp.Body, nil
}

// ErrNoData is a download that ended before a single byte came.
var ErrNoData = errors.New("the download gave nothing")

// Measure reads body until it ends, the limits are reached or ctx is done,
// calling report every `every` with the speed of the last moments. It closes
// body. A download cut short by ctx still has a result if it got far enough
// to have one; the error then is ctx's.
func Measure(ctx context.Context, body io.ReadCloser, lim Limits, every time.Duration, report func(Sample)) (Result, error) {
	start := time.Now()
	type chunk struct {
		n   int
		at  time.Time
		err error
	}
	chunks := make(chan chunk, 64)
	stop := make(chan struct{})
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, err := body.Read(buf)
			select {
			case chunks <- chunk{n, time.Now(), err}:
			case <-stop:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		close(stop)
		body.Close() // unblocks a Read that waits for the network
	}()

	// The settled speed is measured from the end of the warm-up: a tenth of
	// the time limit, a second for the default.
	warmup := lim.Time / 10
	var res Result
	var first, warmAt, last time.Time
	var warmBytes int64
	// Points for the needle: the speed over roughly the last half second.
	type point struct {
		at    time.Time
		bytes int64
	}
	window := []point{}
	tick := time.NewTicker(every)
	defer tick.Stop()
	var limit <-chan time.Time

	finish := func(err error) (Result, error) {
		if first.IsZero() {
			if err == nil || errors.Is(err, io.EOF) {
				err = ErrNoData
			}
			return res, err
		}
		res.Elapsed = last.Sub(first)
		if !warmAt.IsZero() && last.Sub(warmAt) >= warmup {
			res.Mbps = Mbps(res.Bytes-warmBytes, last.Sub(warmAt))
		} else {
			// Over before the warm-up was: a fast server and a small file.
			res.Mbps = Mbps(res.Bytes, res.Elapsed)
		}
		if errors.Is(err, io.EOF) {
			err = nil
		}
		return res, err
	}

	for {
		select {
		case c := <-chunks:
			if c.n > 0 {
				if first.IsZero() {
					first = c.at
					res.TTFB = c.at.Sub(start)
					limit = time.After(lim.Time)
				}
				res.Bytes += int64(c.n)
				last = c.at
				if warmAt.IsZero() && c.at.Sub(first) >= warmup {
					warmAt, warmBytes = c.at, res.Bytes
				}
			}
			if c.err != nil {
				return finish(c.err)
			}
			if lim.Bytes > 0 && res.Bytes >= lim.Bytes {
				return finish(nil)
			}
		case now := <-tick.C:
			if first.IsZero() {
				continue
			}
			window = append(window, point{now, res.Bytes})
			for len(window) > 2 && now.Sub(window[1].at) >= 500*time.Millisecond {
				window = window[1:]
			}
			s := Sample{Bytes: res.Bytes, Elapsed: now.Sub(first)}
			if w := window[0]; now.Sub(w.at) > 0 && len(window) > 1 {
				s.Mbps = Mbps(res.Bytes-w.bytes, now.Sub(w.at))
			} else {
				s.Mbps = Mbps(res.Bytes, now.Sub(first))
			}
			if report != nil {
				report(s)
			}
		case <-limit:
			return finish(nil)
		case <-ctx.Done():
			return finish(ctx.Err())
		}
	}
}
