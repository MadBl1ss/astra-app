package subs

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"strelka/proto"
)

// Every address in these tests is made up (example.com, documentation IPs).

func TestParseList(t *testing.T) {
	type sub = ListEntry
	bom := string(rune(0xFEFF))
	cases := []struct {
		name string
		in   string
		want []sub
	}{
		{"одна ссылка без имени", "https://sub.example.com/abc\n", []sub{{"", "https://sub.example.com/abc"}}},
		{"только комментарии", "# впиши сюда ссылку\n#\n", nil},
		{"пустой файл", "", nil},
		{"комментарии перед ссылкой", "# пояснение\n\nhttps://sub.example.com/x\n", []sub{{"", "https://sub.example.com/x"}}},
		{"CRLF и пробелы", "# note\r\n   https://sub.example.com/y   \r\n", []sub{{"", "https://sub.example.com/y"}}},
		{"решётка внутри URL сохраняется", "https://a/b#frag\n", []sub{{"", "https://a/b#frag"}}},
		{"несколько с именами", "klex https://a/1\n1984   https://b/2\n# x\nlider https://c/3\n",
			[]sub{{"klex", "https://a/1"}, {"1984", "https://b/2"}, {"lider", "https://c/3"}}},
		{"без имени в наборе получает subN", "https://a/1\nx https://b/2\n",
			[]sub{{"sub1", "https://a/1"}, {"x", "https://b/2"}}},
		{"повтор имени", "a https://a/1\na https://b/2\n", []sub{{"a", "https://a/1"}, {"sub2", "https://b/2"}}},
		{"мусор в имени вычищается", "my-vpn! https://a/1\nz https://b/2\n",
			[]sub{{"myvpn", "https://a/1"}, {"z", "https://b/2"}}},
		{"BOM от Блокнота перед комментарием", bom + "# вставь ссылку ниже\n", nil},
		{"BOM перед ссылкой", bom + "https://a/1\n", []sub{{"", "https://a/1"}}},
		{"строка без ссылки не подписка", "вставь сюда\nhttps://a/1\n", []sub{{"", "https://a/1"}}},
	}
	for _, c := range cases {
		got := ParseList(c.in)
		if len(got) != len(c.want) {
			t.Errorf("%s: получил %v, ожидал %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: #%d получил %+v, ожидал %+v", c.name, i, got[i], c.want[i])
			}
		}
	}
}

func TestNameLabel(t *testing.T) {
	cases := []struct{ in, label, country string }{
		{"[FI] Finland", "FI", "FI"},
		{"🇷🇺 [RU3] YouTube без рекламы", "RU3", "RU"},
		{"🇸🇪 [SW] KLEX VPN 🔝TikTok", "SW", "SE"},
		{"🇪🇪 Эстония — 10 Гб/с 🚀", "EE", "EE"},
		{" 🇫🇮 Финляндия (Резерв)", "FI", "FI"},
		{"🇷🇺 Ютуб без рекламы (Подходит для TV)", "RU", "RU"},
		{"🌍 Рекомендуемый сервер", "Рекомендуемый", ""},
		{"10 Гб/с", "Гб", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		l, cc := NameLabel(c.in)
		if l != c.label || cc != c.country {
			t.Errorf("%q: получил (%q, %q), ожидал (%q, %q)", c.in, l, cc, c.label, c.country)
		}
	}
}

func TestParseSS(t *testing.T) {
	ui := base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:s3cr/t+x"))
	legacy := base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pw@192.0.2.4:8388"))

	cases := []struct {
		name, link           string
		method, pass, server string
		port                 int
		plugin, opts, label  string
	}{
		{"SIP002", "ss://" + ui + "@de.example.com:1234#%F0%9F%87%A9%F0%9F%87%AA%20DE",
			"chacha20-ietf-poly1305", "s3cr/t+x", "de.example.com", 1234, "", "", "🇩🇪 DE"},
		{"SIP002 без паддинга и со слешем", "ss://" + strings.TrimRight(ui, "=") + "@h:1/#x",
			"chacha20-ietf-poly1305", "s3cr/t+x", "h", 1, "", "", "x"},
		{"SIP022 открытым текстом", "ss://2022-blake3-aes-128-gcm:a%2Bb%3D@h:443#n",
			"2022-blake3-aes-128-gcm", "a+b=", "h", 443, "", "", "n"},
		{"старый формат", "ss://" + legacy + "#old", "aes-128-gcm", "pw", "192.0.2.4", 8388, "", "", "old"},
		{"плагин", "ss://" + ui + "@h:80/?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dx.com#p",
			"chacha20-ietf-poly1305", "s3cr/t+x", "h", 80, "obfs-local", "obfs=http;obfs-host=x.com", "p"},
	}
	for _, c := range cases {
		ob, label, err := parseSS(c.link)
		if err != nil {
			t.Errorf("%s: ошибка %v", c.name, err)
			continue
		}
		if ob.Method != c.method || ob.Password != c.pass || ob.Server != c.server || ob.ServerPort != c.port ||
			ob.Plugin != c.plugin || ob.PluginOpts != c.opts || label != c.label {
			t.Errorf("%s: получил %+v %q", c.name, *ob, label)
		}
	}
	if _, _, err := parseSS("ss://" + ui + "@h:1?plugin=kcptun#k"); err == nil {
		t.Error("неизвестный плагин должен отклоняться")
	}
}

func TestParseOtherProtocols(t *testing.T) {
	hy, _, err := parseHysteria2("hysteria2://pass@hy.example.com:8443/?sni=hy.example.com&obfs=salamander&obfs-password=o&insecure=1#x")
	if err != nil {
		t.Fatal(err)
	}
	if hy.Type != "hysteria2" || hy.Password != "pass" || hy.ServerPort != 8443 || hy.TLS == nil ||
		!hy.TLS.Enabled || hy.TLS.ServerName != "hy.example.com" || !hy.TLS.Insecure ||
		hy.Obfs == nil || hy.Obfs.Type != "salamander" || hy.Obfs.Password != "o" {
		t.Errorf("hysteria2: %+v tls=%+v obfs=%+v", *hy, hy.TLS, hy.Obfs)
	}

	tr, _, err := parseTrojan("trojan://p%40ss@t.example.com:443?type=ws&path=%2Fws&host=cdn.example.com#t")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Password != "p@ss" || tr.TLS == nil || tr.Transport == nil || tr.Transport.Type != "ws" ||
		tr.Transport.Headers["Host"] != "cdn.example.com" {
		t.Errorf("trojan: %+v", *tr)
	}

	vl, _, err := parseVLESS("vless://id@r.example.com:443?security=reality&sni=a.com&pbk=K&sid=S&type=grpc&serviceName=g#r")
	if err != nil {
		t.Fatal(err)
	}
	if vl.TLS == nil || vl.TLS.Reality == nil || vl.TLS.UTLS == nil || vl.TLS.UTLS.Fingerprint != "chrome" ||
		vl.Transport == nil || vl.Transport.ServiceName != "g" {
		t.Errorf("vless reality без fp должен получить chrome: %+v", vl.TLS)
	}
}

func TestParseLinksSkipsAndTags(t *testing.T) {
	links := []string{
		"vless://id@de1.example.com:443?security=tls&type=tcp#%F0%9F%87%A9%F0%9F%87%AA%20Germany",
		"vless://id@de2.example.com:443?security=tls&type=tcp#%F0%9F%87%A9%F0%9F%87%AA%20Germany%202",
		"vless://id@lv.example.com:443?security=tls&type=xhttp&path=%2F#%F0%9F%87%B1%F0%9F%87%BB%20Latvia",
		"vless://id@ru.example.com:443?security=tls#%F0%9F%87%B7%F0%9F%87%BA%20Russia",
		"vless://00000000-0000-0000-0000-000000000000@0.0.0.0:1?security=none#App%20not%20supported",
		"vmess://eyJ2IjoiMiJ9",
		"vless://id@x.example.com:443?encryption=mlkem768x25519plus.native.0rtt.abc#pq",
	}
	got, skipped := ParseLinks(links, "lider", ReservedTags(), false)

	var tags []string
	byTag := map[string]Server{}
	for _, s := range got {
		tags = append(tags, s.ID)
		byTag[s.ID] = s
	}
	want := "lider-DE lider-DE2 lider-RU"
	if strings.Join(tags, " ") != want {
		t.Errorf("теги %v, ожидал %s", tags, want)
	}
	if len(skipped) != 4 {
		t.Errorf("ожидал 4 причины пропуска (xhttp, заглушка, vmess, encryption), получил %+v", skipped)
	}

	de := byTag["lider-DE"]
	if de.Name != "🇩🇪 Germany" || de.Country != "DE" || de.Protocol != "vless" || de.Via != proto.ViaSingBox ||
		de.Host != "de1.example.com" || de.Port != 443 || de.Xray != nil {
		t.Errorf("lider-DE: %+v", de)
	}
	var ob map[string]any
	if err := json.Unmarshal(de.Outbound, &ob); err != nil || ob["tag"] != "lider-DE" || ob["server"] != "de1.example.com" {
		t.Errorf("выход sing-box: %s (%v)", de.Outbound, err)
	}
	if byTag["lider-RU"].Country != "RU" {
		t.Errorf("страна RU не распознана: %+v", byTag["lider-RU"])
	}
}

func TestUniqueTagAvoidsReserved(t *testing.T) {
	used := ReservedTags()
	if got := UniqueTag("proxy", used); got != "proxy2" {
		t.Errorf("сервер с именем proxy получил тег %q", got)
	}
	used["DE2"] = 1 // a server literally named DE2 came first
	UniqueTag("DE", used)
	if got := UniqueTag("DE", used); got != "DE3" {
		t.Errorf("второй DE получил %q, ожидал DE3", got)
	}
}

// Remnawave shuffles the list per request; tags must not follow the shuffle.
func TestParseLinksStableUnderShuffle(t *testing.T) {
	links := []string{
		"vless://id@a.example.com:443?security=tls#%F0%9F%87%A9%F0%9F%87%AA%20Germany%2011",
		"vless://id@b.example.com:443?security=tls#%F0%9F%87%A9%F0%9F%87%AA%20Germany%202",
		"hysteria2://p@c.example.com:8443/?sni=c#%F0%9F%87%B5%F0%9F%87%B1%20Poland%20RH2",
		"vless://id@d.example.com:443?security=tls#%F0%9F%87%B5%F0%9F%87%B1%20Poland%2010",
	}
	mapping := func(order []int) map[string]string {
		var ls []string
		for _, i := range order {
			ls = append(ls, links[i])
		}
		m := map[string]string{}
		got, _ := ParseLinks(ls, "x", map[string]int{}, false)
		for _, s := range got {
			m[s.ID] = s.Host
		}
		return m
	}
	a, b := mapping([]int{0, 1, 2, 3}), mapping([]int{3, 2, 1, 0})
	if len(a) != 4 {
		t.Fatalf("серверов %d, ожидал 4", len(a))
	}
	for tag, srv := range a {
		if b[tag] != srv {
			t.Errorf("%s: %s в одном порядке и %s в другом", tag, srv, b[tag])
		}
	}
}

func TestParseLinksRoutesToXray(t *testing.T) {
	links := []string{
		"vless://id@r.example.com:443?security=reality&sni=a&pbk=K#%F0%9F%87%A9%F0%9F%87%AA%20R",
		"vless://id@x.example.com:443?security=tls&type=xhttp#%F0%9F%87%B1%F0%9F%87%BB%20X",
		"vless://id@w.example.com:443?security=tls&type=ws#%F0%9F%87%B3%F0%9F%87%B1%20W",
	}
	got, _ := ParseLinks(links, "p", map[string]int{}, true)
	if len(got) != 3 {
		t.Fatalf("серверов %d, ожидал 3", len(got))
	}
	via := map[string]string{}
	for _, s := range got {
		via[s.ID] = s.Via
		if s.Via == proto.ViaXray {
			var xo map[string]any
			if err := json.Unmarshal(s.Xray, &xo); err != nil || xo["tag"] != s.ID {
				t.Errorf("%s: у Xray-outbound не тот тег: %s", s.ID, s.Xray)
			}
			if s.Outbound != nil {
				t.Errorf("%s: у сервера через Xray не должно быть выхода sing-box", s.ID)
			}
			if s.Host == "" || s.Host == "127.0.0.1" {
				t.Errorf("%s: хост должен быть настоящим адресом сервера, а там %q", s.ID, s.Host)
			}
		}
	}
	if via["p-DE"] != proto.ViaXray || via["p-LV"] != proto.ViaXray || via["p-NL"] != proto.ViaSingBox {
		t.Errorf("распределение: %v", via)
	}

	// Without the xray binary REALITY falls back to sing-box and XHTTP is dropped.
	got, _ = ParseLinks(links, "p", map[string]int{}, false)
	if len(got) != 2 || got[0].Xray != nil || got[1].Xray != nil {
		t.Errorf("без xray: %+v", got)
	}
}
