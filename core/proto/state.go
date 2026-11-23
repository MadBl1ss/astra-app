// Package proto holds the wire types shared by strelkad and its clients.
// A change goes here and to app/src/lib/proto.ts at once.
package proto

import "time"

// Version is the protocol number. An incompatible change bumps it.
const Version = 1

// Desired is what the user asked for; it survives crashes and reboots.
const (
	DesiredOn  = "on"
	DesiredOff = "off"
)

// Phase is what the engine is actually doing.
const (
	PhaseOff        = "off"
	PhaseStarting   = "starting"
	PhaseOn         = "on"
	PhaseRecovering = "recovering"
	PhaseStopping   = "stopping"
	PhaseFailed     = "failed"
)

const (
	ModeRule   = "rule"
	ModeGlobal = "global"
)

// Who talks to the server: the engine itself or the Xray helper.
const (
	ViaSingBox = "sing-box"
	ViaXray    = "xray"
)

// Fail reasons for vpn.failReason.
const (
	FailConfig    = "config"
	FailTUN       = "tun"
	FailPort      = "port"
	FailCrashLoop = "crashloop"
	FailMissing   = "missing"
)

// Health verdicts.
const (
	VerdictOK               = "ok"
	VerdictSettling         = "settling"
	VerdictDegraded         = "degraded"
	VerdictServerDown       = "server_down"
	VerdictAllServersDown   = "all_servers_down"
	VerdictNoInternet       = "no_internet"
	VerdictNoNetwork        = "no_network"
	VerdictEngineRecovering = "engine_recovering"
	VerdictEngineFailed     = "engine_failed"
	VerdictHelperDown       = "helper_down"
)

// Values of State.busy.
const (
	BusyStarting     = "starting"
	BusyStopping     = "stopping"
	BusyUpdatingSubs = "updating_subs"
	BusyTesting      = "testing"
	BusyMeasuring    = "measuring" // a speed test
)

// Values of servers[].check: where a server is in the test under way.
const (
	CheckQueued  = "queued"
	CheckRunning = "running"
	CheckDone    = "done"
)

// Values of test.stage and speed.stage.
const (
	TestPreparing = "preparing" // the test's own engine is coming up
	TestTesting   = "testing"
)

// Values of speed.phase: what is being done to the server being measured.
const (
	SpeedPing     = "ping"
	SpeedDownload = "download"
)

// State is the one document the daemon broadcasts in full on every change.
// Slices are never nil on the wire: clients index them without null checks.
type State struct {
	Proto     int        `json:"proto"`
	Version   string     `json:"version"`
	Rev       uint64     `json:"rev"`
	VPN       VPN        `json:"vpn"`
	Health    Health     `json:"health"`
	Guard     Guard      `json:"guard"`
	Server    Selected   `json:"server"`
	Servers   []Server   `json:"servers"`
	Subs      []Sub      `json:"subs"`
	Traffic   Traffic    `json:"traffic"`
	Conns     Conns      `json:"conns"`
	Incidents []Incident `json:"incidents"`
	Settings  Settings   `json:"settings"`
	Busy      *string    `json:"busy"`
	// Test is the server test under way; nil when there is none.
	Test *TestRun `json:"test"`
	// Speed is the speed test under way; nil when there is none.
	Speed *SpeedRun `json:"speed"`
	// Place is where the computer is, for the globe on the window's Home;
	// nil while it is not known.
	Place *Place `json:"place"`
}

// Place is one end of the globe's beam.
type Place struct {
	Lat     float64   `json:"lat"`
	Lon     float64   `json:"lon"`
	City    string    `json:"city"` // what to show; may be empty
	Country *string   `json:"country"`
	Source  string    `json:"source"` // ip | manual
	At      time.Time `json:"at"`     // when it was found or chosen
}

// Values of place.source.
const (
	PlaceIP     = "ip"
	PlaceManual = "manual"
)

type VPN struct {
	Desired    string     `json:"desired"`
	Phase      string     `json:"phase"`
	Since      *time.Time `json:"since"` // when phase became "on"
	Mode       string     `json:"mode"`
	FailReason *string    `json:"failReason"`
	FailText   *string    `json:"failText"`
}

type Health struct {
	Verdict  string     `json:"verdict"`
	Since    *time.Time `json:"since"`
	Hint     *string    `json:"hint"`
	Internet bool       `json:"internet"`
	Network  bool       `json:"network"`
}

type Guard struct {
	Enabled  bool `json:"enabled"`
	Engaged  bool `json:"engaged"`
	Blocking bool `json:"blocking"`
	AllowLAN bool `json:"allowLan"`
}

// Selected is the server the selector points at.
type Selected struct {
	Selected *string    `json:"selected"`
	Ping     *int       `json:"ping"`
	PingAt   *time.Time `json:"pingAt"`
}

type Server struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Country  *string      `json:"country"`
	Sub      string       `json:"sub"`
	Protocol string       `json:"protocol"`
	Via      string       `json:"via"`
	Host     string       `json:"host"`
	Favorite bool         `json:"favorite"`
	Order    int          `json:"order"`
	Ping     *int         `json:"ping"`
	PingAt   *time.Time   `json:"pingAt"`
	Alive    bool         `json:"alive"`
	Stats    *ServerStats `json:"stats"`
	// Check is where the server is in the test under way: queued, running,
	// done; nil when it is not part of one.
	Check *string `json:"check"`
}

// TestRun is the progress of a server test.
type TestRun struct {
	Started time.Time `json:"started"`
	Stage   string    `json:"stage"` // preparing | testing
	// Own: the test runs on an engine of its own, since the VPN is off.
	Own   bool `json:"own"`
	Total int  `json:"total"`
	Done  int  `json:"done"`  // answered or timed out
	Alive int  `json:"alive"` // answered
}

// ServerStats is what the daemon has learnt about a server over time.
type ServerStats struct {
	Avg     int     `json:"avg"`     // typical delay, ms; 0 when it never answered
	Jitter  int     `json:"jitter"`  // how much the delay wobbles around that, ms
	Loss    float64 `json:"loss"`    // share of checks without an answer, 0..1
	Samples int     `json:"samples"` // answers counted
	// Score is delay, wobble, answers and speed together, 0..100; Trust,
	// 0..1, how much of it is evidence rather than a guess. A server with
	// little evidence scores close to 50 whatever it showed.
	Score int     `json:"score"`
	Trust float64 `json:"trust"`
	Days  int     `json:"days"` // days with answers in the last two weeks
	// Stable: it has answered on several days, nearly always, evenly.
	Stable bool `json:"stable"`
	// Speed is the typical download speed, Mbit/s; nil until measured.
	Speed   *float64   `json:"speed"`
	SpeedAt *time.Time `json:"speedAt"`
}

// SpeedRun is a speed test under way: one server after another, each pinged
// a few times and then made to download for a few seconds.
type SpeedRun struct {
	Started time.Time `json:"started"`
	Stage   string    `json:"stage"` // preparing | testing
	// Own: the test runs on an engine of its own, since the VPN is off.
	Own bool     `json:"own"`
	IDs []string `json:"ids"` // in the order they are measured
	// Current is the server being measured and Phase what is being done to
	// it; both nil between servers.
	Current *string `json:"current"`
	Phase   *string `json:"phase"`
	// Results are the servers measured so far, in order.
	Results []SpeedResult `json:"results"`
	// The bounds of one download, for the window to word what it costs.
	MaxBytes   int64 `json:"maxBytes"`
	MaxSeconds int   `json:"maxSeconds"`
}

// SpeedResult is how one server did in a speed test.
type SpeedResult struct {
	ID     string   `json:"id"`
	Ping   *int     `json:"ping"`   // the middle of its pings; nil: it did not answer
	Jitter *int     `json:"jitter"` // how far its pings were from that
	Mbps   *float64 `json:"mbps"`   // nil: the download failed
	Bytes  int64    `json:"bytes"`  // traffic the download took
	Error  *string  `json:"error"`  // why a number is missing, for the user
}

type Sub struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	URL        string     `json:"url"` // always masked
	Servers    int        `json:"servers"`
	UpdatedAt  *time.Time `json:"updatedAt"`
	Error      *string    `json:"error"`
	ExpireAt   *time.Time `json:"expireAt"`
	UsedBytes  *int64     `json:"usedBytes"`
	TotalBytes *int64     `json:"totalBytes"`
}

type Traffic struct {
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	UpTotal   int64 `json:"upTotal"`
	DownTotal int64 `json:"downTotal"`
}

type Conns struct {
	Total int `json:"total"`
	VPN   int `json:"vpn"`
}

type Incident struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Severity string    `json:"severity"`
	Opened   time.Time `json:"opened"`
	Server   *string   `json:"server"`
	Title    string    `json:"title"`
	Body     string    `json:"body"`
	Flaps    int       `json:"flaps"`
	Unstable bool      `json:"unstable"`
	// Hidden: the window draws no card for it - the user closed the card or
	// turned such warnings off. Never set on a critical incident.
	Hidden bool `json:"hidden"`
}

// The values of Settings.IPv6.
const (
	// IPv6Tunnel takes IPv6 into the TUN, so that it cannot go around the
	// VPN. On a network without IPv6 the TUN gets none either: there is
	// nothing to go around, and a TUN with an IPv6 address would make
	// programs believe in IPv6 that leads nowhere.
	IPv6Tunnel = "tunnel"
	// IPv6Block gives the TUN no IPv6 address, and the engine blocks IPv6.
	IPv6Block = "block"
)

type Settings struct {
	Notify string `json:"notify"`
	// SubWarnings and ServerWarnings turn whole kinds of warnings off: no
	// toast and no card, only the journal. A subscription about to end is
	// something the user may well know already and cannot fix here.
	SubWarnings     bool   `json:"subWarnings"`
	ServerWarnings  bool   `json:"serverWarnings"`
	QuietFullscreen bool   `json:"quietFullscreen"`
	Guard           bool   `json:"guard"`
	AllowLAN        bool   `json:"allowLan"`
	IPv6            string `json:"ipv6"`
	Failover        bool   `json:"failover"`
	AutoUpdateSubs  string `json:"autoUpdateSubs"`
	LocalProxyPort  int    `json:"localProxyPort"`
	StartOnBoot     bool   `json:"startOnBoot"`
	// Rating: while the VPN works, a few servers are pinged every so often
	// to keep their rating fresh.
	Rating bool `json:"rating"`
}

// DefaultSettings are the values of a fresh install.
func DefaultSettings() Settings {
	return Settings{
		Notify:          "important",
		SubWarnings:     true,
		ServerWarnings:  true,
		QuietFullscreen: true,
		Guard:           true,
		AllowLAN:        true,
		IPv6:            IPv6Tunnel,
		Failover:        false,
		AutoUpdateSubs:  "12h",
		LocalProxyPort:  0,
		StartOnBoot:     true,
		Rating:          true,
	}
}

// NewState is the document of a daemon that has done nothing yet.
func NewState(version string) State {
	settings := DefaultSettings()
	return State{
		Proto:   Version,
		Version: version,
		VPN:     VPN{Desired: DesiredOff, Phase: PhaseOff, Mode: ModeRule},
		Health:  Health{Verdict: VerdictSettling},
		// guard.enabled and guard.allowLan repeat the settings; whether the
		// filters are actually in place is "engaged".
		Guard:     Guard{Enabled: settings.Guard, AllowLAN: settings.AllowLAN},
		Servers:   []Server{},
		Subs:      []Sub{},
		Incidents: []Incident{},
		Settings:  settings,
	}
}

// JournalEntry is one line of the event journal.
type JournalEntry struct {
	ID       int64     `json:"id"`
	T        time.Time `json:"t"`
	Cat      string    `json:"cat"`   // vpn | server | subs | rules | guard | system
	Level    string    `json:"level"` // info | problem | critical
	Text     string    `json:"text"`
	Incident *string   `json:"incident"`
}

const (
	CatVPN    = "vpn"
	CatServer = "server"
	CatSubs   = "subs"
	CatRules  = "rules"
	CatGuard  = "guard"
	CatSystem = "system"

	LevelInfo     = "info"
	LevelProblem  = "problem"
	LevelCritical = "critical"
)

// Incident kinds. The first four repeat the verdict they grew out of.
const (
	IncidentServerDown     = VerdictServerDown
	IncidentAllServersDown = VerdictAllServersDown
	IncidentEngineFailed   = VerdictEngineFailed
	IncidentHelperDown     = VerdictHelperDown
	// IncidentBlocked: the kill switch has kept the internet closed for a
	// while and the engine is still not back. It is not a verdict - the
	// engine is restarting, which by itself is no trouble.
	IncidentBlocked     = "blocked"
	IncidentSubExpiring = "sub_expiring" // the subscription ends within three days
	IncidentSubExpired  = "sub_expired"
	IncidentSubTraffic  = "sub_traffic" // the traffic allowance is used up
)

// Severities of incidents and toasts.
const (
	SeverityCritical = "critical"
	SeverityProblem  = "problem"
	SeverityInfo     = "info"
)

// Values of settings.notify.
const (
	NotifyAll       = "all"
	NotifyImportant = "important"
	NotifySilent    = "silent"
)

// LogLine is one line of an engine's or the daemon's own log.
type LogLine struct {
	T      time.Time `json:"t"`
	Source string    `json:"source"` // sing-box | xray | strelkad
	Level  string    `json:"level"`
	Line   string    `json:"line"`
}
