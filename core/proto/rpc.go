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
