package proto

import (
	"encoding/json"
	"fmt"
	"time"
)

// Methods the daemon answers at this stage.
const (
	MethodHello            = "hello"
	MethodStateGet         = "state.get"
	MethodStateSubscribe   = "state.subscribe"
	MethodStateUnsubscribe = "state.unsubscribe"
	MethodVPNStart         = "vpn.start"
	MethodVPNStop          = "vpn.stop"
	MethodVPNRestart       = "vpn.restart"
	MethodEngineStatus     = "engine.status"
)

// Topics double as the method names of the notifications they carry.
const (
	TopicState       = "state"
	TopicJournal     = "journal"
	TopicTraffic     = "traffic"
	TopicConnections = "connections"
	TopicToast       = "toast"
	TopicLog         = "log"
)

// KnownTopic reports whether a client may subscribe to topic.
func KnownTopic(topic string) bool {
	switch topic {
	case TopicState, TopicJournal, TopicTraffic, TopicConnections, TopicToast, TopicLog:
		return true
	}
	return false
}

// Error codes. Message is always a ready Russian text for the user.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeBusy           = 1001
	CodeNoServers      = 1002
	CodeNotFound       = 1003
	CodeInvalid        = 1004
	CodeFetchFailed    = 1005
	CodeEngine         = 1006
	CodeUnsupported    = 1007
)

// Message is one JSON-RPC 2.0 object. A single struct covers requests,
// responses and notifications so a connection can decode a line before it
// knows which of the three it is.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// IsNotification is true for a message with a method and no id.
func (m *Message) IsNotification() bool { return m.Method != "" && len(m.ID) == 0 }

type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

// Errorf builds an error with a user-facing text.
func Errorf(code int, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

type HelloParams struct {
	Client  string `json:"client"` // app | ctl
	Version string `json:"version"`
	Proto   int    `json:"proto"`
}

type HelloResult struct {
	Version string `json:"version"`
	Proto   int    `json:"proto"`
	OS      string `json:"os"`
}

type TopicsParams struct {
	Topics []string `json:"topics"`
}

// EngineStatusResult is the answer to the debug method engine.status.
type EngineStatusResult struct {
	Engines []EngineStatus `json:"engines"`
}

type EngineStatus struct {
	Name     string     `json:"name"` // sing-box | xray
	Path     string     `json:"path"`
	Running  bool       `json:"running"`
	PID      int        `json:"pid"`      // 0 when not running
	Since    *time.Time `json:"since"`    // start of the current process
	Restarts int        `json:"restarts"` // crashes since the last vpn.start
}

// Methods added with profiles, rules and settings.
const (
	MethodModeSet        = "mode.set"
	MethodServerSelect   = "server.select"
	MethodServerTest     = "server.test"
	MethodServerFavorite = "server.favorite"
	MethodServerReorder  = "server.reorder"
	MethodServerRemove   = "server.remove"
	MethodSubsAdd        = "subs.add"
	MethodSubsAddLink    = "subs.addLink"
	MethodSubsRename     = "subs.rename"
	MethodSubsRemove     = "subs.remove"
	MethodSubsUpdate     = "subs.update"
	MethodRulesGet       = "rules.get"
	MethodRulesAdd       = "rules.add"
	MethodRulesRemove    = "rules.remove"
	MethodRulesCheck     = "rules.check"
	MethodRulesProcesses = "rules.processes"
	MethodSettingsSet    = "settings.set"
	MethodLegacyImport   = "legacy.import"
)

type ModeParams struct {
	Mode string `json:"mode"` // rule | global
}

type IDParams struct {
	ID string `json:"id"`
}

type ServerTestParams struct {
	IDs []string `json:"ids,omitempty"` // empty means every server
}

type ServerFavoriteParams struct {
	ID       string `json:"id"`
	Favorite bool   `json:"favorite"`
}

type ServerReorderParams struct {
	IDs []string `json:"ids"`
}

type SubsAddParams struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
}

// SubsAddResult tells the client what the new subscription brought.
type SubsAddResult struct {
	ID      string `json:"id"`
	Servers int    `json:"servers"`
}

type SubsAddLinkParams struct {
	Link string `json:"link"`
}

type SubsRenameParams struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SubsUpdateParams struct {
	ID string `json:"id,omitempty"` // empty means all
}

// Rule lists and kinds.
const (
	ListVPN    = "vpn"
	ListDirect = "direct"

	KindProcess = "process"
	KindDomain  = "domain"
	KindSuffix  = "suffix"
	KindKeyword = "keyword"
	KindIP      = "ip"
)

// RuleList is one direction's rules. Slices are never nil on the wire.
type RuleList struct {
	Processes []string `json:"processes"`
	Domains   []string `json:"domains"`
	Suffixes  []string `json:"suffixes"`
	Keywords  []string `json:"keywords"`
	IPs       []string `json:"ips"`
}

// AutoList describes one of the lists the engine keeps up to date itself.
type AutoList struct {
	Tag       string     `json:"tag"`
	Count     int        `json:"count"`
	UpdatedAt *time.Time `json:"updatedAt"`
}

type RulesGetResult struct {
	VPN    RuleList   `json:"vpn"`
	Direct RuleList   `json:"direct"`
	Auto   []AutoList `json:"auto"`
}

type RulesAddParams struct {
	List  string `json:"list"`
	Value string `json:"value"`
	Kind  string `json:"kind,omitempty"` // empty: the daemon works it out
}

// RuleRef names one rule; it is both the answer to rules.add and the
// parameters of rules.remove.
type RuleRef struct {
	List  string `json:"list,omitempty"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type RulesCheckParams struct {
	Target string `json:"target"`
}

type RulesCheckResult struct {
	Route   string `json:"route"` // vpn | direct
	Because string `json:"because"`
}

type ProcessInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type SettingsSetParams struct {
	Patch json.RawMessage `json:"patch"`
}

type LegacyImportParams struct {
	Dir string `json:"dir"`
}

// LegacyImportResult sums up what came over from the old Strelka.
type LegacyImportResult struct {
	Subs     int      `json:"subs"`
	Servers  int      `json:"servers"`
	Rules    int      `json:"rules"`
	Selected *string  `json:"selected"`
	Mode     string   `json:"mode"`
	Failed   []string `json:"failed"` // subscriptions that did not download
}

// Connection is one live connection in the "connections" notification.
type Connection struct {
	ID      string   `json:"id"`
	Process string   `json:"process"`
	Host    string   `json:"host"`
	Dest    string   `json:"dest"`
	Route   string   `json:"route"` // vpn | direct
	Chain   []string `json:"chain"`
	Up      int64    `json:"up"`
	Down    int64    `json:"down"`
	Started string   `json:"started"`
}

// TrafficSample is the "traffic" notification: bytes per second.
type TrafficSample struct {
	T    time.Time `json:"t"`
	Up   int64     `json:"up"`
	Down int64     `json:"down"`
}
