package health

import "testing"

// Lines in the shape the engines write them; hosts, addresses and server
// names are made up.
func TestParseLine(t *testing.T) {
	cases := []struct {
		source, line string
		server       string
		hint         Hint
	}{
		{"sing-box", `+0300 2026-11-14 20:46:11 ERROR [2291 5.0s] connection: open connection to example.com:443 using outbound/vless[t-DE]: dial tcp 192.0.2.10:443: i/o timeout`,
			"t-DE", HintUnreachable},
		{"sing-box", `+0300 2026-11-14 20:46:12 ERROR [2292 21ms] connection: open connection to example.com:443 using outbound/trojan[t-NL]: dial tcp 192.0.2.11:443: connectex: No connection could be made because the target machine actively refused it.`,
			"t-NL", HintUnreachable},
		{"sing-box", `+0300 2026-11-14 20:46:13 ERROR [2293 21.0s] connection: open connection to example.com:443 using outbound/vless[t-DE]: dial tcp 192.0.2.10:443: connectex: A connection attempt failed because the connected party did not properly respond after a period of time, or established connection failed because connected host has failed to respond.`,
			"t-DE", HintUnreachable},
		{"sing-box", `+0300 2026-11-14 20:46:14 ERROR [2294 80ms] connection: open connection to example.com:443 using outbound/vless[t-DE]: reality verification failed`,
			"t-DE", HintKeys},
		{"sing-box", `+0300 2026-11-14 20:46:15 ERROR [2295 90ms] connection: open connection to example.com:443 using outbound/hysteria2[t-FI]: authentication failed, status code: 404`,
			"t-FI", HintAuth},
		{"sing-box", `+0300 2026-11-14 20:46:16 ERROR [2296 70ms] connection: open connection to example.com:443 using outbound/vless[t-DE]: tls handshake: EOF`,
			"t-DE", HintReset},
		{"sing-box", `+0300 2026-11-14 20:46:17 ERROR [2297 70ms] connection: open connection to example.com:443 using outbound/trojan[t-NL]: tls handshake: read tcp 192.168.1.5:50001->192.0.2.11:443: wsarecv: An existing connection was forcibly closed by the remote host.`,
			"t-NL", HintReset},
		{"xray", `2026/11/14 20:46:18.100000 [Warning] [1234] app/proxyman/outbound: failed to process outbound traffic > proxy/vless/outbound: failed to find an available destination > common/retry: [dial tcp 192.0.2.12:443: i/o timeout] > common/retry: all retry attempts failed`,
			"", HintUnreachable},
		{"xray", `2026/11/14 20:46:19.100000 [Info] [1235] transport/internet/tcp: REALITY: processed invalid connection`,
			"", HintKeys},
	}
	for _, c := range cases {
		server, hint, ok := ParseLine(c.source, c.line)
		if !ok || server != c.server || hint != c.hint {
			t.Errorf("%s: %q\n  получил сервер %q, подсказку %q, ok=%v; жду %q и %q", c.source, c.line, server, hint, ok, c.server, c.hint)
		}
	}
}

// What the engine logs all day long while everything works. None of it is
// about a server.
func TestParseLineIgnoresTheEverydayNoise(t *testing.T) {
	for _, line := range []string{
		`+0300 2026-11-14 20:46:11 ERROR [2291 1.2s] connection: connection upload closed: raw-read tcp4 192.168.1.5:50001->192.0.2.20:443: An existing connection was forcibly closed by the remote host.`,
		`+0300 2026-11-14 20:46:11 ERROR [2292 300ms] connection: open connection to [2001:db8::1]:443 using outbound/direct[direct]: dial tcp [2001:db8::1]:443: connectex: A socket operation was attempted to an unreachable network.`,
		`+0300 2026-11-14 20:46:11 ERROR [2293 2ms] connection: open connection to 192.0.2.30:80 using outbound/direct[direct]: dial tcp 192.0.2.30:80: connectex: No connection could be made because the target machine actively refused it.`,
		`+0300 2026-11-14 20:46:11 INFO [2294 0ms] outbound/vless[t-DE]: outbound connection to example.com:443`,
		`+0300 2026-11-14 20:46:11 INFO router: updated default interface Wi-Fi, index 12`,
		`WARN[0000] implicit default HTTP client using default outbound for remote rule-sets is deprecated`,
	} {
		if server, hint, ok := ParseLine("sing-box", line); ok {
			t.Errorf("строка принята за ошибку сервера %q (%s): %s", server, hint, line)
		}
	}
	for _, line := range []string{
		`2026/11/14 20:46:18.100000 [Warning] core: Xray 26.3.27 started`,
		`2026/11/14 20:46:18.100000 [Info] infra/conf/serial: Reading config: &{Name:xray.json Format:json}`,
	} {
		if _, hint, ok := ParseLine("xray", line); ok {
			t.Errorf("строка Xray принята за ошибку (%s): %s", hint, line)
		}
	}
	if _, _, ok := ParseLine("strelkad", "ERROR outbound/vless[t-DE]: i/o timeout"); ok {
		t.Error("строка чужого источника разобрана")
	}
}

func TestFailReason(t *testing.T) {
	cases := []struct {
		name string
		tail []string
		want string
	}{
		{"адаптер без прав", []string{
			`INFO network: updated default interface Ethernet, index 7`,
			`FATAL[0000] start service: start inbound/tun[tun-in]: configure tun interface: Access is denied.`,
		}, "tun"},
		{"адаптер занят", []string{
			`FATAL[0000] start service: start inbound/tun[tun-in]: create tun: Cannot create a file when that file already exists.`,
		}, "tun"},
		{"порт занят", []string{
			`FATAL[0000] start service: start inbound/mixed[mixed-in]: listen tcp 127.0.0.1:20808: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.`,
		}, "port"},
		{"порт занят, Linux", []string{
			`FATAL[0000] start service: start inbound/mixed[mixed-in]: listen tcp 127.0.0.1:20808: bind: address already in use`,
		}, "port"},
		{"что-то другое", []string{`FATAL fake engine: crashing as scripted`}, ""},
		{"пусто", nil, ""},
	}
	for _, c := range cases {
		if got := FailReason(c.tail); got != c.want {
			t.Errorf("%s: причина %q, жду %q", c.name, got, c.want)
		}
	}
}
