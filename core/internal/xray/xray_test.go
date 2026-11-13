package xray

import (
	"encoding/json"
	"strings"
	"testing"

	"strelka/internal/testjson"
)

func TestNeeds(t *testing.T) {
	cases := map[string]bool{
		"security=reality&type=tcp":         true,
		"security=tls&type=xhttp":           true,
		"security=tls&type=ws":              false,
		"security=none&type=grpc":           false,
		"encryption=mlkem768x25519plus.x":   true,
		"security=tls&type=tcp&encryption=": false,
	}
	for q, want := range cases {
		u, _, _, _ := SplitLink("vless://id@h:443?" + q)
		if got := Needs(u.Query()); got != want {
			t.Errorf("%s: получил %v, ожидал %v", q, got, want)
		}
	}
}

func TestOutbound(t *testing.T) {
	reality := "vless://uid@r.example.com:443?encryption=none&flow=xtls-rprx-vision&type=grpc&serviceName=g&security=reality&sni=a.com&fp=edge&pbk=PBK&sid=ab12&spx=%2F" +
		`&fm=%7B%22tcp%22%3A%5B%7B%22type%22%3A%22fragment%22%7D%5D%7D#x`
	ob, name, err := Outbound(reality)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(ob)
	for path, want := range map[string]string{
		"protocol":                                 "vless",
		"settings.vnext.0.address":                 "r.example.com",
		"settings.vnext.0.port":                    "443",
		"settings.vnext.0.users.0.id":              "uid",
		"settings.vnext.0.users.0.flow":            "xtls-rprx-vision",
		"streamSettings.network":                   "grpc",
		"streamSettings.grpcSettings.serviceName":  "g",
		"streamSettings.security":                  "reality",
		"streamSettings.realitySettings.publicKey": "PBK",
		"streamSettings.realitySettings.shortId":   "ab12",
		"streamSettings.realitySettings.spiderX":   "/",
		"streamSettings.finalmask.tcp.0.type":      "fragment",
	} {
		if got := testjson.Get(b, path); got != want {
			t.Errorf("%s = %q, ожидал %q", path, got, want)
		}
	}
	if name != "x" {
		t.Errorf("имя %q", name)
	}

	xhttp := "vless://uid@x.example.com:443?encryption=none&type=xhttp&path=%2Fmedia%2F&host=x.example.com&mode=packet-up" +
		"&extra=%7B%22xPaddingBytes%22%3A%2248-300%22%7D&security=tls&sni=cdn.example.net&fp=firefox&alpn=h2%2Chttp%2F1.1#y"
	ob, _, err = Outbound(xhttp)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(ob)
	for path, want := range map[string]string{
		"streamSettings.network":                           "xhttp",
		"streamSettings.xhttpSettings.path":                "/media/",
		"streamSettings.xhttpSettings.mode":                "packet-up",
		"streamSettings.xhttpSettings.extra.xPaddingBytes": "48-300",
		"streamSettings.tlsSettings.serverName":            "cdn.example.net",
		"streamSettings.tlsSettings.alpn.1":                "http/1.1",
		"streamSettings.tlsSettings.fingerprint":           "firefox",
	} {
		if got := testjson.Get(b, path); got != want {
			t.Errorf("xhttp %s = %q, ожидал %q", path, got, want)
		}
	}
}

func TestBuild(t *testing.T) {
	b, err := Build([]Server{
		{Tag: "a-DE", Port: 17100, Outbound: json.RawMessage(`{"tag":"a-DE","protocol":"vless"}`)},
		{Tag: "b-LV", Port: 17101, Outbound: json.RawMessage(`{"tag":"b-LV","protocol":"vless"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if testjson.Get(b, "inbounds.1.port") != "17101" || testjson.Get(b, "inbounds.1.listen") != "127.0.0.1" ||
		testjson.Get(b, "inbounds.0.settings.udp") != "true" {
		t.Errorf("inbounds: %s", testjson.Get(b, "inbounds"))
	}
	rule := testjson.Find(b, "routing.rules", "outboundTag", "b-LV")
	if testjson.Get(rule, "inboundTag.0") != "in-b-LV" {
		t.Errorf("routing: %s", testjson.Get(b, "routing"))
	}
	if !strings.Contains(string(b), `"protocol": "freedom"`) {
		t.Error("нет direct")
	}
	// The daemon reads Xray's stderr; a log file would hide a failed start.
	if testjson.Get(b, "log.error") != "" {
		t.Errorf("лог Xray должен идти в stderr: %s", testjson.Get(b, "log"))
	}
}

func TestAssignPortsKeepsServersInPlace(t *testing.T) {
	blocked := map[int]bool{BasePort + 1: true} // a port Windows reserved
	free := func(p int) bool { return !blocked[p] }

	first, err := AssignPorts([]string{"a", "b", "c"}, nil, free)
	if err != nil {
		t.Fatal(err)
	}
	if first["a"] != BasePort || first["b"] != BasePort+2 || first["c"] != BasePort+3 {
		t.Errorf("первая раздача: %v", first)
	}

	// "a" left the subscription, "d" joined; "b" and "c" are in use and now
	// look busy to the probe, yet they must stay where they are.
	blocked[BasePort+2], blocked[BasePort+3] = true, true
	second, err := AssignPorts([]string{"b", "c", "d"}, first, free)
	if err != nil {
		t.Fatal(err)
	}
	if second["b"] != first["b"] || second["c"] != first["c"] {
		t.Errorf("серверы переехали: было %v, стало %v", first, second)
	}
	if second["d"] != BasePort {
		t.Errorf("новый сервер должен занять освободившийся порт %d, а получил %d", BasePort, second["d"])
	}
	seen := map[int]string{}
	for tag, p := range second {
		if other, dup := seen[p]; dup {
			t.Errorf("порт %d выдан дважды: %s и %s", p, other, tag)
		}
		seen[p] = tag
	}
}
