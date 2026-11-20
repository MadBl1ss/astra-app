package subs

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Unwrapped is a subscription address dug out of a link made for some app.
type Unwrapped struct {
	URL  string // the http(s) address to download
	Name string // a name the wrapper suggested; "" when it had none
}

// maxWraps bounds how many wrappers Unwrap peels. A bot's button is a
// redirect page around a Happ link around the address: two. Nobody nests
// deeper than a few, and a loop must end.
const maxWraps = 4

// What Unwrap refuses with. The texts go to the user as they are.
var (
	ErrNotSubLink   = errors.New("Это не похоже на ссылку подписки. Скопируй её у провайдера целиком: она начинается с https:// или с названия приложения, например happ://.")
	ErrNoSubInside  = errors.New("В этой ссылке не нашлось адреса подписки. Попроси у провайдера обычную ссылку подписки — она начинается с https://.")
	ErrServerInside = errors.New("В этой ссылке один сервер, а не подписка. Попроси у провайдера ссылку подписки или ссылку самого сервера — она начинается с vless://, trojan://, ss:// или hysteria2://.")
)

// EncryptedError is a link the provider encrypted so that only its own app
// reads it: Happ's happ://crypt…/, INCY's incy://crypt1/. The keys live in
// those apps; taking them out is not something Strelka does.
type EncryptedError struct{ App string }

func (e *EncryptedError) Error() string {
	return fmt.Sprintf("Провайдер зашифровал эту ссылку для приложения %s — прочитать её может только оно. "+
		"Попроси у провайдера обычную ссылку подписки: в боте она обычно в разделе «Другие приложения» "+
		"или называется «Ссылка для v2rayN».", e.App)
}

// RoutingError is a link to an app's own routing rules (happ://routing/…),
// which bots hand out next to the subscription.
type RoutingError struct{ App string }

func (e *RoutingError) Error() string {
	return fmt.Sprintf("Это ссылка на правила для приложения %s, а не подписка. Нужна ссылка подписки от провайдера.", e.App)
}

// serverSchemes are share links of one server: inside a wrapper they mean
// the provider handed out a server, not a subscription.
var serverSchemes = map[string]bool{
	"vless": true, "vmess": true, "trojan": true, "ss": true, "ssr": true,
	"hysteria": true, "hysteria2": true, "hy2": true, "tuic": true,
	"wireguard": true, "wg": true, "socks": true, "socks5": true, "anytls": true,
}

// linkParams are the query parameters wrappers carry the inner link in:
// redirect pages (?link=), Clash and v2rayNG (?url=), Loon (?sub=).
var linkParams = []string{"url", "link", "sub", "config", "redirect", "redirect_to"}

var appNames = map[string]string{
	"happ": "Happ", "incy": "INCY", "v2raytun": "v2RayTun", "hiddify": "Hiddify",
	"streisand": "Streisand", "v2rayng": "v2rayNG", "karing": "Karing",
}

func appName(scheme string) string {
	if n, ok := appNames[scheme]; ok {
		return n
	}
	return scheme
}

// Unwrap turns what a provider hands out for its favourite app into the
// subscription address Strelka downloads: a redirect page with the link in
// a parameter, happ://add/<address>, sing-box://import-remote-profile?url=…,
// sub://<base64> and the like, nested up to maxWraps deep, the inner link
// plain, percent-encoded or base64. A plain http(s) address comes back as
// it is, so an address added before keeps matching itself.
//
// It parses rather than searches the text for "https://": a search would
// happily take an address out of an encrypted link's padding or a page's
// own query and download something that is not the subscription.
func Unwrap(raw string) (Unwrapped, error) {
	var out Unwrapped
	link := strings.TrimSpace(raw)
	for wraps := 0; ; wraps++ {
		scheme, rest, ok := splitScheme(link)
		if !ok {
			return out, ErrNotSubLink
		}
		var inner, name string
		switch {
		case scheme == "http" || scheme == "https":
			inner, name = fromPage(link)
			if inner == "" {
				return finish(out, link, wraps)
			}
		case serverSchemes[scheme]:
			return out, ErrServerInside
		default:
			var err error
			if inner, name, err = fromApp(scheme, rest); err != nil {
				return out, err
			}
		}
		// The outermost name wins: it is the one the provider wrote for
		// people; the inner ones were made for apps.
		if out.Name == "" {
			out.Name = name
		}
		if wraps == maxWraps {
			return out, ErrNoSubInside
		}
		link = inner
	}
}

// finish checks the address a chain of wrappers ended in.
func finish(out Unwrapped, link string, wraps int) (Unwrapped, error) {
	if wraps > 0 {
		// A fragment never reaches the server. In a wrapped address it is
		// the subscription's name: hiddify://import/https://…#Имя, Happ's
		// "#Имя?fallback-url=…", v2rayNG's url=…%23Имя.
		if base, frag, ok := strings.Cut(link, "#"); ok {
			link = base
			if out.Name == "" {
				out.Name = fragName(frag)
			}
		}
	}
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" {
		if wraps > 0 {
			return out, ErrNoSubInside
		}
		return out, ErrNotSubLink
	}
	out.URL = link
	return out, nil
}

// fromPage reads a redirect page that names the link it leads to in its
// query: https://panel/redirect?link=happ://add/https://…. "" when the page
// carries no link, which is what a subscription address itself looks like.
func fromPage(link string) (inner, name string) {
	u, err := url.Parse(link)
	if err != nil || u.RawQuery == "" {
		return "", ""
	}
	q := u.Query()
	for _, k := range linkParams {
		if in, n, ok := decodeLink(q.Get(k)); ok {
			if n == "" {
				// An inner link left unencoded loses its fragment to the
				// page: …?link=hiddify://import/https://…#Имя.
				n = fragName(u.EscapedFragment())
			}
			return in, n
		}
	}
	return "", ""
}

// fromApp reads a link made for an app: scheme://action/<link>,
// scheme://action?url=<link>, or the link itself in place of the action.
func fromApp(scheme, rest string) (inner, name string, err error) {
	action, after := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		action, after = rest[:i], rest[i:]
	}
	switch act := strings.ToLower(action); {
	case strings.HasPrefix(act, "crypt"): // crypt, crypt2…crypt5, crypt1
		return "", "", &EncryptedError{App: appName(scheme)}
	case act == "routing" || act == "autorouting":
		return "", "", &RoutingError{App: appName(scheme)}
	}

	// Path form: happ://add/<link>, v2raytun://import/<link>,
	// hiddify://import/<link>#Имя, incy://import/<base64>. The inner link
	// runs to the end, its own query and fragment included.
	if p, ok := strings.CutPrefix(after, "/"); ok && p != "" && !strings.HasPrefix(p, "?") {
		if in, n, ok := decodeLink(p); ok {
			return in, n, nil
		}
	}

	// Query form: sing-box://import-remote-profile?url=…#Имя,
	// clash://install-config?url=…&name=…, hiddify://install-sub/?url=….
	if _, tail, ok := strings.Cut(after, "?"); ok {
		query, frag, _ := strings.Cut(tail, "#")
		q, _ := url.ParseQuery(query) // what parsed is enough
		for _, k := range linkParams {
			if in, n, ok := decodeLink(q.Get(k)); ok {
				if v := strings.TrimSpace(q.Get("name")); v != "" {
					n = v
				} else if f := fragName(frag); f != "" {
					n = f
				}
				return in, n, nil
			}
		}
	}

	// The link in place of the action: Shadowrocket's sub://<base64>.
	if in, n, ok := decodeLink(rest); ok {
		return in, n, nil
	}
	return "", "", ErrNoSubInside
}

// decodeLink takes a link out of a wrapper's parameter: as it is,
// percent-encoded once more, or in base64 with an optional "#name" after.
func decodeLink(v string) (link, name string, ok bool) {
	raw := v
	v = strings.TrimSpace(v)
	if v == "" {
		return "", "", false
	}
	if looksLikeLink(v) {
		return v, "", true
	}
	body, frag, _ := strings.Cut(v, "#")
	name = fragName(frag)
	if strings.Contains(body, "%") {
		if s, err := url.PathUnescape(body); err == nil && looksLikeLink(s) {
			return s, name, true
		}
	}
	// A query turns base64's "+" into a blank, a trailing one too, which
	// the trimming above dropped; without it the rest still decodes, one
	// character short. So the untrimmed text goes first.
	rawBody, _, _ := strings.Cut(raw, "#")
	for _, b := range []string{rawBody, body} {
		if s, err := decodeB64(strings.ReplaceAll(b, " ", "+")); err == nil {
			if s = strings.TrimSpace(s); utf8.ValidString(s) && looksLikeLink(s) {
				return s, name, true
			}
		}
	}
	return "", "", false
}

// looksLikeLink: "scheme://something" with no blanks before the fragment,
// which is a name and may have them ("#My VPN" pasted unencoded).
func looksLikeLink(s string) bool {
	_, rest, ok := splitScheme(s)
	base, _, _ := strings.Cut(s, "#")
	return ok && rest != "" && !strings.ContainsAny(base, " \t\r\n")
}

// splitScheme cuts "scheme://rest" and lowercases the scheme.
func splitScheme(s string) (scheme, rest string, ok bool) {
	i := strings.Index(s, "://")
	if i <= 0 {
		return "", "", false
	}
	scheme = strings.ToLower(s[:i])
	for j, r := range scheme {
		letter := r >= 'a' && r <= 'z'
		if !letter && (j == 0 || !(r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.')) {
			return "", "", false
		}
	}
	return scheme, s[i+3:], true
}

// fragName reads a name out of a fragment: percent-encoded as a rule, with
// Happ's options after it ("Имя?fallback-url=…").
func fragName(frag string) string {
	if s, err := url.PathUnescape(frag); err == nil {
		frag = s
	}
	frag, _, _ = strings.Cut(frag, "?")
	return strings.TrimSpace(frag)
}
