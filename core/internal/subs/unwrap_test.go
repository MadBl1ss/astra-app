package subs

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// Every address here is made up.
const subAddr = "https://panel.example.com/sub/abc123"

func b64(s string) string    { return base64.StdEncoding.EncodeToString([]byte(s)) }
func b64url(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
func enc(s string) string    { return url.QueryEscape(s) }

func TestUnwrapFindsTheSubscription(t *testing.T) {
	cases := []struct {
		what, in, url, name string
	}{
		{"plain address stays as it is", subAddr, subAddr, ""},
		{"plain address keeps its fragment", subAddr + "#Мой", subAddr + "#Мой", ""},
		{"plain address keeps its query", "https://panel.example.com/api/v1/client/subscribe?token=abc123",
			"https://panel.example.com/api/v1/client/subscribe?token=abc123", ""},
		{"blanks around", "  " + subAddr + "\n", subAddr, ""},
		{"http works too", "http://panel.example.com/sub/abc123", "http://panel.example.com/sub/abc123", ""},

		// What the bots' buttons lead to.
		{"redirect page around Happ", "https://panel.example.com/redirect?link=happ://add/" + subAddr, subAddr, ""},
		{"redirect page, encoded", "https://panel.example.com/redirect?link=" + enc("happ://add/"+subAddr), subAddr, ""},
		{"redirect page, double encoded", "https://panel.example.com/redirect?link=" + enc(enc("happ://add/"+subAddr)), subAddr, ""},
		{"redirect page, base64", "https://panel.example.com/redirect?link=" + b64("happ://add/"+subAddr), subAddr, ""},
		{"redirect page, base64url", "https://panel.example.com/redirect?link=" + b64url("happ://add/"+subAddr), subAddr, ""},
		{"redirect page straight to the address", "https://panel.example.com/redirect?url=" + enc(subAddr), subAddr, ""},
		{"redirect page, unencoded name lands on the page", "https://panel.example.com/redirect?link=hiddify://import/" + subAddr + "#Мой VPN",
			subAddr, "Мой VPN"},

		// Each app's own form.
		{"happ", "happ://add/" + subAddr, subAddr, ""},
		{"happ in capitals", "HAPP://ADD/" + subAddr, subAddr, ""},
		{"happ, encoded address", "happ://add/" + enc(subAddr), subAddr, ""},
		{"happ keeps the address's query", "happ://add/https://panel.example.com/sub?token=abc123&flag=1",
			"https://panel.example.com/sub?token=abc123&flag=1", ""},
		{"happ, name with its options", "happ://add/" + subAddr + "#VPN?fallback-url=x", subAddr, "VPN"},
		{"v2raytun", "v2raytun://import/" + subAddr, subAddr, ""},
		{"hiddify import with name", "hiddify://import/" + subAddr + "#%D0%9C%D0%BE%D0%B9%20VPN", subAddr, "Мой VPN"},
		{"hiddify import, name unencoded", "hiddify://import/" + subAddr + "#Мой VPN", subAddr, "Мой VPN"},
		{"hiddify install-sub", "hiddify://install-sub?url=" + enc(subAddr) + "#Мой", subAddr, "Мой"},
		{"streisand", "streisand://import/" + subAddr + "#Мой", subAddr, "Мой"},
		{"incy add", "incy://add/" + subAddr, subAddr, ""},
		{"incy import, base64", "incy://import/" + b64(subAddr), subAddr, ""},
		{"sing-box", "sing-box://import-remote-profile?url=" + enc(subAddr) + "#%D0%9C%D0%BE%D0%B9", subAddr, "Мой"},
		{"sing-box, unencoded", "sing-box://import-remote-profile?url=" + subAddr + "#Мой", subAddr, "Мой"},
		{"clash with name", "clash://install-config?url=" + enc(subAddr) + "&name=Provider", subAddr, "Provider"},
		{"clash, name beats fragment", "clash://install-config?url=" + enc(subAddr) + "&name=Provider#Other", subAddr, "Provider"},
		{"clash keeps the address's query", "clash://install-config?url=" + enc("https://panel.example.com/sub?token=a&b=c"),
			"https://panel.example.com/sub?token=a&b=c", ""},
		{"clashmeta", "clashmeta://install-config?url=" + enc(subAddr), subAddr, ""},
		{"v2rayng install-config", "v2rayng://install-config?url=" + enc(subAddr) + "&name=Мой", subAddr, "Мой"},
		{"v2rayng install-sub, name inside", "v2rayng://install-sub/?url=" + enc(subAddr+"#Мой"), subAddr, "Мой"},
		{"karing", "karing://install-config?url=" + enc(subAddr) + "&name=Мой", subAddr, "Мой"},
		{"loon sub=", "loon://import?sub=" + enc(subAddr), subAddr, ""},
		{"shadowrocket sub://base64", "sub://" + b64(subAddr) + "#Мой", subAddr, "Мой"},
		{"shadowrocket sub://plain", "sub://" + subAddr, subAddr, ""},
		{"base64 with + turned into a blank", "clash://install-config?url=" + strings.ReplaceAll(b64(subAddr+"?a=>>>"), "+", "%20"),
			subAddr + "?a=>>>", ""},

		// Wrappers inside wrappers.
		{"three deep", "https://panel.example.com/redirect?link=" + enc("shadowrocket://add/sub://"+b64(subAddr)), subAddr, ""},
		{"four deep", "https://panel.example.com/r?link=" + enc("hiddify://import/"+"happ://add/"+"sub://"+b64(subAddr)), subAddr, ""},
		{"outer name wins", "clash://install-config?url=" + enc("hiddify://import/"+subAddr+"#Inner") + "&name=Outer", subAddr, "Outer"},
	}
	for _, c := range cases {
		got, err := Unwrap(c.in)
		if err != nil {
			t.Errorf("%s: %q: %v", c.what, c.in, err)
			continue
		}
		if got.URL != c.url || got.Name != c.name {
			t.Errorf("%s: %q\n got  %q %q\n want %q %q", c.what, c.in, got.URL, got.Name, c.url, c.name)
		}
	}
}

func TestUnwrapRefuses(t *testing.T) {
	var encrypted *EncryptedError
	var routing *RoutingError
	cases := []struct {
		what, in string
		is       func(error) bool
		app      string
	}{
		{"happ crypt", "happ://crypt/QUJD", func(err error) bool { return errors.As(err, &encrypted) }, "Happ"},
		{"happ crypt4", "happ://crypt4/QUJDREVG", func(err error) bool { return errors.As(err, &encrypted) }, "Happ"},
		{"happ crypt5", "happ://crypt5/QUJDREVG", func(err error) bool { return errors.As(err, &encrypted) }, "Happ"},
		{"incy crypt1", "incy://crypt1/QUJDREVG", func(err error) bool { return errors.As(err, &encrypted) }, "INCY"},
		{"crypt behind a redirect page", "https://panel.example.com/redirect?link=" + enc("happ://crypt5/QUJDREVG"),
			func(err error) bool { return errors.As(err, &encrypted) }, "Happ"},
		{"happ routing", "happ://routing/onadd/" + b64("{}"), func(err error) bool { return errors.As(err, &routing) }, "Happ"},
		{"a server, not a subscription", "happ://add/vless://id@vpn.example.com:443#DE", is(ErrServerInside), ""},
		{"a bare server link", "vless://id@vpn.example.com:443#DE", is(ErrServerInside), ""},
		{"nothing after add/", "happ://add/", is(ErrNoSubInside), ""},
		{"not a link after add/", "happ://add/hello", is(ErrNoSubInside), ""},
		{"an app's other button", "incy://connect", is(ErrNoSubInside), ""},
		{"unknown app with nothing", "foo://bar", is(ErrNoSubInside), ""},
		{"address without a host inside", "happ://add/https://", is(ErrNoSubInside), ""},
		{"five deep", "https://a.example.com/r?link=" + enc("clash://install-config?url="+enc("hiddify://import/"+
			"happ://add/"+"sub://"+b64(subAddr))), is(ErrNoSubInside), ""},
		{"empty", "", is(ErrNotSubLink), ""},
		{"a word", "привет", is(ErrNotSubLink), ""},
		{"no scheme", "panel.example.com/sub/abc123", is(ErrNotSubLink), ""},
		{"broken scheme", "ht tp://panel.example.com/sub", is(ErrNotSubLink), ""},
		{"https without a host", "https://", is(ErrNotSubLink), ""},
	}
	for _, c := range cases {
		got, err := Unwrap(c.in)
		if err == nil || !c.is(err) {
			t.Errorf("%s: %q: got %+v, %v", c.what, c.in, got, err)
			continue
		}
		if c.app != "" {
			app := ""
			switch {
			case errors.As(err, &encrypted):
				app = encrypted.App
			case errors.As(err, &routing):
				app = routing.App
			}
			if app != c.app || !strings.Contains(err.Error(), c.app) {
				t.Errorf("%s: приложение %q, текст %q", c.what, app, err)
			}
		}
	}
	if !strings.Contains((&EncryptedError{App: "Happ"}).Error(), "обычную ссылку подписки") {
		t.Error("текст про шифрование не говорит, что попросить у провайдера")
	}
}

func is(target error) func(error) bool {
	return func(err error) bool { return errors.Is(err, target) }
}
