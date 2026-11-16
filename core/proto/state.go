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
}

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
}

type ServerStats struct {
	Avg     int     `json:"avg"`
	Loss    float64 `json:"loss"`
	Samples int     `json:"samples"`
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
}

type Settings struct {
	Notify          string `json:"notify"`
	QuietFullscreen bool   `json:"quietFullscreen"`
	Guard           bool   `json:"guard"`
	AllowLAN        bool   `json:"allowLan"`
	IPv6            string `json:"ipv6"`
	Failover        bool   `json:"failover"`
	AutoUpdateSubs  string `json:"autoUpdateSubs"`
	LocalProxyPort  int    `json:"localProxyPort"`
	StartOnBoot     bool   `json:"startOnBoot"`
}

// DefaultSettings are the values of a fresh install.
func DefaultSettings() Settings {
	return Settings{
		Notify:          "important",
		QuietFullscreen: true,
		Guard:           true,
		AllowLAN:        true,
		IPv6:            "tunnel",
		Failover:        false,
		AutoUpdateSubs:  "12h",
		LocalProxyPort:  0,
		StartOnBoot:     true,
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
	CatSystem = "system"

	LevelInfo     = "info"
	LevelProblem  = "problem"
	LevelCritical = "critical"
)

// LogLine is one line of an engine's or the daemon's own log.
type LogLine struct {
	T      time.Time `json:"t"`
	Source string    `json:"source"` // sing-box | xray | strelkad
	Level  string    `json:"level"`
	Line   string    `json:"line"`
}
