package health

import "time"

// Tuning is every threshold and interval of the health rules in one place.
// The automata take it as a value, so a test can run the same logic on
// millisecond-sized pauses; production uses Default and nothing else.
type Tuning struct {
	// ---- probing the selected server ------------------------------------

	// ProbeEvery is the calm pace. A probe is a real request through the
	// server: often enough to notice a dead one within half a minute, rare
	// enough to cost nothing on a metered link.
	ProbeEvery time.Duration
	// TrafficFresh: bytes that came back through the VPN this recently are
	// better proof of life than any probe, so the probe is skipped.
	TrafficFresh time.Duration
	// ProbeTimeout is how long one probe may take. 3 s used to fail a server
	// over a single slow handshake; the engine parses the value as int16
	// milliseconds, so it cannot go much higher either.
	ProbeTimeout time.Duration
	// RetryAt are the re-checks after a first miss, counted from that miss.
	// They come quickly because one miss says nothing yet.
	RetryAt []time.Duration
	// RetryEvery paces further re-checks when the list above ran out before
	// the evidence was long enough (a server that refuses at once).
	RetryEvery time.Duration
	// ConfirmMisses in a row, spanning at least ConfirmSpan with no traffic
	// in between, make a server "down". The span is what keeps three instant
	// refusals during a Wi-Fi reconnect from counting: they fit in a second.
	ConfirmMisses int
	ConfirmSpan   time.Duration
	// DownProbeEvery is the pace while the server is down: the user is
	// waiting for it to come back, so faster than the calm pace.
	DownProbeEvery time.Duration
	// RecoverOKs successes in a row end "down". One could be a fluke of a
	// server that answers every other time; RecoverRetry is how soon the
	// second one is asked for.
	RecoverOKs   int
	RecoverRetry time.Duration

	// ---- telling the server from the network ----------------------------

	// SettleAfterNet: after the network changes or the machine wakes, the
	// adapter, DHCP and the tunnel need a moment. Nothing is diagnosed and
	// nothing is probed for this long.
	SettleAfterNet time.Duration
	// DirectFresh is how long an answer of the probe that bypasses the VPN
	// stays valid. Shorter than the confirmation takes, so the verdict never
	// leans on what the internet was like before the trouble began.
	DirectFresh time.Duration
	// OthersMax other servers are tried to tell "this server" from "all of
	// them"; OthersFresh is how long that answer stands while the trouble
	// lasts. Five is enough to be sure and still cheap.
	OthersMax   int
	OthersFresh time.Duration
	// SleepGap between two ticks means the machine slept: what was being
	// counted before is stale.
	SleepGap time.Duration
	// NetDebounce merges the burst of adapter and route events one network
	// change produces into a single observation.
	NetDebounce time.Duration
	// HintFresh is how long an error seen in an engine's log may explain an
	// outage. Older lines are about some other moment.
	HintFresh time.Duration

	// ---- "works, but badly" ----------------------------------------------

	// Window is how many last probes "degraded" looks at; DegradedMisses of
	// them lost (30 %) is unstable, and it takes dropping to DegradedClear
	// (10 %) to call it fine again, so the verdict does not flicker at the
	// threshold.
	Window         int
	DegradedMisses int
	DegradedClear  int
	// A delay SlowFactor times the usual and above SlowFloor, lasting
	// SlowFor, is "degraded" too. The floor keeps a 40 ms server that
	// answers in 130 ms from counting; the duration, a single slow answer.
	SlowFactor float64
	SlowFloor  time.Duration
	SlowFor    time.Duration

	// ---- incidents -------------------------------------------------------

	// CloseAfterOK: an incident ends when "ok" has held this long.
	CloseAfterOK time.Duration
	// ReopenWithin: the same trouble this soon after it ended is the same
	// incident coming back, not a new one.
	ReopenWithin time.Duration
	// UnstableFlaps reopenings inside UnstableWindow mark the incident
	// unstable; it then stays open until CalmFor passes without trouble.
	UnstableFlaps  int
	UnstableWindow time.Duration
	CalmFor        time.Duration
	// Grace follows anything the user did (stop, restart, another server, a
	// subscription refresh): what breaks in this window is the action's own
	// doing, and a problem is reported only if it outlives the window.
	Grace time.Duration
	// SubWarnBefore the subscription runs out the user is told once.
	SubWarnBefore time.Duration
}

// Default is the tuning the service ships with.
func Default() Tuning {
	return Tuning{
		ProbeEvery:     30 * time.Second,
		TrafficFresh:   30 * time.Second,
		ProbeTimeout:   5 * time.Second,
		RetryAt:        []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second},
		RetryEvery:     5 * time.Second,
		ConfirmMisses:  3,
		ConfirmSpan:    12 * time.Second,
		DownProbeEvery: 10 * time.Second,
		RecoverOKs:     2,
		RecoverRetry:   2 * time.Second,

		SettleAfterNet: 15 * time.Second,
		DirectFresh:    15 * time.Second,
		OthersMax:      5,
		OthersFresh:    time.Minute,
		SleepGap:       30 * time.Second,
		NetDebounce:    time.Second,
		HintFresh:      time.Minute,

		Window:         20,
		DegradedMisses: 6,
		DegradedClear:  2,
		SlowFactor:     3,
		SlowFloor:      800 * time.Millisecond,
		SlowFor:        2 * time.Minute,

		CloseAfterOK:   10 * time.Second,
		ReopenWithin:   5 * time.Minute,
		UnstableFlaps:  3,
		UnstableWindow: 10 * time.Minute,
		CalmFor:        5 * time.Minute,
		Grace:          20 * time.Second,
		SubWarnBefore:  72 * time.Hour,
	}
}

// Where the probes go. Two addresses of each kind: one target being down
// must not look like a connection problem, so a retry asks the other one.
var (
	// DirectURLs are fetched past the VPN, through the engine's "direct"
	// outbound. Plain HTTP on purpose: these are the captive-portal checks of
	// Windows and Android, they answer anywhere and need no TLS handshake.
	DirectURLs = [2]string{
		"http://www.msftconnecttest.com/connecttest.txt",
		"http://connectivitycheck.gstatic.com/generate_204",
	}
	// ServerURLs are fetched through a server.
	ServerURLs = [2]string{
		"http://www.gstatic.com/generate_204",
		"http://cp.cloudflare.com/generate_204",
	}
)
