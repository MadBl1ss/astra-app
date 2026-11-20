package subs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
)

// browserHeaders are what a desktop browser sends when it opens a page.
// Short-link services in front of subscription pages answer anything less,
// apps included, with 404 or a redirect to nowhere.
var browserHeaders = map[string]string{
	"User-Agent":                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36",
	"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	"Accept-Language":           "ru-RU,ru;q=0.9,en;q=0.8",
	"Sec-Fetch-Mode":            "navigate",
	"Sec-Fetch-Dest":            "document",
	"Sec-Fetch-Site":            "none",
	"Sec-Fetch-User":            "?1",
	"Upgrade-Insecure-Requests": "1",
}

// errNoSubOnPage: the address is a web page and none of its buttons led to
// a subscription.
var errNoSubOnPage = errors.New("по ссылке открывается страница провайдера, а не подписка, и адреса подписки на ней не нашлось — открой её в браузере и скопируй ссылку подписки")

// fromWebPage opens addr as a browser does and returns the subscription the
// page offers to its visitors' apps. Some bots hand out a short link to
// such a page (the button "Add to Happ" is on it) instead of the
// subscription. The page is asked for without the device headers: it is
// not the panel, and the panel is who counts devices.
func fromWebPage(ctx context.Context, hc *http.Client, addr string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", addr, nil)
	if err != nil {
		return "", errNoSubOnPage
	}
	for k, v := range browserHeaders {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", errNoSubOnPage
	}
	defer resp.Body.Close()
	found := ""
	switch {
	case resp.StatusCode/100 == 3:
		// The page's server may send a browser straight to the app.
		u, err := Unwrap(resp.Header.Get("Location"))
		if err != nil {
			return "", appLinkError(err)
		}
		found = u.URL
	case resp.StatusCode == http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return "", errNoSubOnPage
		}
		found = subFromPage(string(body))
	}
	if found == "" || found == addr {
		return "", errNoSubOnPage
	}
	return found, nil
}

// pageApps are the schemes of the app buttons on subscription pages. Plain
// http(s) addresses on a page are not looked at: they are its scripts,
// stores and support chats, and nothing tells the subscription among them.
var pageApps = map[string]bool{
	"happ": true, "incy": true, "v2raytun": true, "hiddify": true, "streisand": true,
	"v2rayng": true, "sing-box": true, "clash": true, "clashmeta": true, "flclash": true,
	"flclashx": true, "karing": true, "stash": true, "shadowrocket": true, "sub": true,
	"koala-clash": true, "prizrak-box": true,
}

// subFromPage finds the subscription on a provider's page by its app
// buttons: each "<app>://…" in the page is unwrapped as a pasted link
// would be, and the address most of them lead to wins, so one odd button
// (an address made for Clash alone) does not. A button the page completes
// in a script ('happ://add/' + url) unwraps to nothing and is skipped. ""
// when no button leads anywhere.
func subFromPage(page string) string {
	// Addresses sit in attributes and in script strings.
	page = strings.NewReplacer("&amp;", "&", `\/`, "/", `&`, "&", `=`, "=").Replace(page)
	count := map[string]int{}
	var order []string
	for rest := page; ; {
		i := strings.Index(rest, "://")
		if i < 0 {
			break
		}
		start := i
		for start > 0 && isSchemeByte(rest[start-1]) {
			start--
		}
		end := i + 3
		for end < len(rest) && !strings.ContainsRune("\"'`<> \t\r\n\\", rune(rest[end])) {
			end++
		}
		scheme, cand := strings.ToLower(rest[start:i]), rest[start:end]
		// Scanning goes on right after "://": a button inside another
		// address (?link=happ://…) is seen too.
		rest = rest[i+3:]
		if !pageApps[scheme] {
			continue
		}
		u, err := Unwrap(cand)
		if err != nil {
			continue
		}
		if count[u.URL] == 0 {
			order = append(order, u.URL)
		}
		count[u.URL]++
	}
	best := ""
	for _, u := range order {
		if count[u] > count[best] {
			best = u
		}
	}
	return best
}

func isSchemeByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '+' || b == '-' || b == '.'
}
