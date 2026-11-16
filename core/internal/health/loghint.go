package health

import (
	"regexp"
	"strings"
)

// The engines say why a connection failed, each in its own words. This file
// is the dictionary. It only ever produces a guess ("похоже, …"): a timeout
// in a log does not prove the server is gone, it is merely what a gone
// server looks like.

// outboundTag finds the server a sing-box line is about:
// "... using outbound/vless[lider-DE]: dial tcp ...".
var outboundTag = regexp.MustCompile(`outbound/[a-z0-9_-]+\[([^\]]+)\]`)

// hintTable is tried top to bottom, so the specific causes come before the
// generic "did not answer". Windows words its socket errors differently from
// everyone else; both spellings are listed.
var hintTable = []struct {
	re   *regexp.Regexp
	hint Hint
}{
	{regexp.MustCompile(`(?i)reality verification failed|REALITY: processed invalid connection`), HintKeys},
	{regexp.MustCompile(`(?i)authentication failed|invalid user|invalid password|unknown user|auth(?:entication)? error`), HintAuth},
	{regexp.MustCompile(`(?i)(?:tls|handshake|reality|utls)[^:]*:.*(?:\bEOF\b|connection reset|forcibly closed)|` +
		`(?:\bEOF\b|connection reset|forcibly closed).*(?:tls|handshake)`), HintReset},
	{regexp.MustCompile(`(?i)i/o timeout|connection refused|actively refused|did not properly respond|` +
		`no route to host|network is unreachable|unreachable network|unreachable host|context deadline exceeded`), HintUnreachable},
}

// ParseLine classifies one line of an engine's log. server is the tag of the
// server the line is about; Xray's lines do not carry one and come back with
// an empty server, which the caller reads as "whichever is in use, if it
// goes through Xray".
func ParseLine(source, line string) (server string, hint Hint, ok bool) {
	switch source {
	case "sing-box":
		// Only failures to reach a server matter. The engine logs every
		// connection a program dropped as an ERROR too, hundreds a minute.
		m := outboundTag.FindStringSubmatch(line)
		if m == nil || m[1] == "direct" || !strings.Contains(line, "ERROR") {
			return "", HintNone, false
		}
		server = m[1]
	case "xray":
		if !strings.Contains(line, "outbound") && !strings.Contains(line, "REALITY") {
			return "", HintNone, false
		}
	default:
		return "", HintNone, false
	}
	for _, row := range hintTable {
		if row.re.MatchString(line) {
			return server, row.hint, true
		}
	}
	return "", HintNone, false
}

// failTable reads the reason of a crash loop off the engine's last words.
var failTable = []struct {
	re     *regexp.Regexp
	reason string
}{
	// The adapter cannot be created: another VPN holds it, the driver is
	// missing, or the daemon lacks the rights.
	{regexp.MustCompile(`(?i)configure tun interface|create tun|open tun|start inbound/tun|wintun`), "tun"},
	{regexp.MustCompile(`(?i)listen (?:tcp|udp)[^:]*:?.*(?:bind:|address already in use|only one usage of each socket)`), "port"},
}

// FailReason looks at the tail of an engine's output for a known cause of
// its refusing to start: "tun", "port", or "" when nothing is recognised.
func FailReason(tail []string) string {
	// The last lines are the ones about the failure; start from the end.
	for i := len(tail) - 1; i >= 0; i-- {
		for _, row := range failTable {
			if row.re.MatchString(tail[i]) {
				return row.reason
			}
		}
	}
	return ""
}
