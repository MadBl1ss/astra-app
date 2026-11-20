package profile

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"strelka/internal/subs"
	"strelka/proto"
)

var t0 = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

func links(names ...string) subs.Fetched {
	var out []string
	for _, n := range names {
		host := strings.ToLower(n) + ".example.com"
		out = append(out, "vless://id@"+host+":443?security=tls#["+n+"] server")
	}
	return subs.Fetched{Links: out}
}

func ids(p *Profile) []string {
	var out []string
	for _, s := range p.Servers {
		out = append(out, s.ID)
	}
	return out
}

func mustAdd(t *testing.T, p *Profile, url, name string, f subs.Fetched) *Sub {
	t.Helper()
	sub, err := p.AddSub(url, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.ApplyFetch(sub.ID, f, false, t0); err != nil {
		t.Fatal(err)
	}
	return p.Sub(sub.ID)
}

func TestAddSubValidatesAndNames(t *testing.T) {
	p := New()
	for _, bad := range []string{"", "sub.example.com/abc", "ftp://sub.example.com/x", "vless://id@h:443"} {
		var inv *Invalid
		if _, err := p.AddSub(bad, ""); !errors.As(err, &inv) {
			t.Errorf("%q: ожидал отказ с текстом для пользователя, получил %v", bad, err)
		}
	}
	a, err := p.AddSub("  https://sub.example.com/abc  ", "lider")
	if err != nil || a.ID != "s1" || a.Prefix != "lider" || a.URL != "https://sub.example.com/abc" {
		t.Fatalf("первая подписка: %+v, %v", a, err)
	}
	if _, err := p.AddSub("https://sub.example.com/abc", "again"); !errors.Is(err, ErrExists) {
		t.Errorf("та же ссылка второй раз: %v", err)
	}
	// A name with nothing Latin in it cannot prefix a tag; the id does.
	b, _ := p.AddSub("https://other.example.com/x", "Мой провайдер")
	if b.Prefix != "s2" || b.Name != "Мой провайдер" || b.AutoName {
		t.Errorf("русское имя: %+v", b)
	}
	// A taken prefix is not reused: two subscriptions would fight over ids.
	c, _ := p.AddSub("https://third.example.com/x", "lider")
	if c.Prefix != "s3" {
		t.Errorf("повтор приставки: %+v", c)
	}
	d, _ := p.AddSub("https://fourth.example.com/x", "")
	if d.Name != "fourth.example.com" || !d.AutoName {
		t.Errorf("без имени: %+v", d)
	}
}

func TestApplyFetchKeepsIDsAndSurvivesFailures(t *testing.T) {
	p := New()
	a := mustAdd(t, p, "https://a.example.com/1", "a", links("DE", "NL", "FI"))
	mustAdd(t, p, "https://b.example.com/1", "b", links("DE", "SE"))
	want := []string{"a-DE", "a-FI", "a-NL", "b-DE", "b-SE"}
	if !slices.Equal(ids(p), want) {
		t.Fatalf("серверы %v, ожидал %v", ids(p), want)
	}
	if a.UpdatedAt == nil || a.Error != "" || p.CountOf("s1") != 3 {
		t.Errorf("после обновления: %+v", a)
	}

	// A refresh that brings nothing usable keeps the old servers.
	later := t0.Add(time.Hour)
	_, _, err := p.ApplyFetch("s1", subs.Fetched{Links: []string{"vmess://x", "vless://id@0.0.0.0:1#stub"}}, false, later)
	if err == nil {
		t.Fatal("пустое обновление должно быть ошибкой")
	}
	if !slices.Equal(ids(p), want) {
		t.Errorf("пустое обновление потеряло серверы: %v", ids(p))
	}
	if a := p.Sub("s1"); a.Error == "" || !a.TriedAt.Equal(later) || !a.UpdatedAt.Equal(t0) {
		t.Errorf("ошибка обновления не записана: %+v", a)
	}
	p.FetchFailed("s2", errors.New("сервер подписки не ответил вовремя"), later)
	if p.Sub("s2").Error == "" || p.CountOf("s2") != 2 {
		t.Errorf("провал скачивания: %+v", p.Sub("s2"))
	}

	// The provider reshuffled its list and dropped a server: ids of the
	// ones that stayed do not move, and the other subscription is untouched.
	if _, _, err := p.ApplyFetch("s1", links("NL", "DE"), false, later); err != nil {
		t.Fatal(err)
	}
	if got := ids(p); !slices.Equal(got, []string{"a-DE", "a-NL", "b-DE", "b-SE"}) {
		t.Errorf("после перетасовки: %v", got)
	}
	if p.Server("a-DE").Host != "de.example.com" || p.Sub("s1").Error != "" {
		t.Errorf("a-DE: %+v, подписка: %+v", p.Server("a-DE"), p.Sub("s1"))
	}
}

func TestFetchMetaAndTitle(t *testing.T) {
	p := New()
	sub, _ := p.AddSub("https://sub.example.com/abc", "")
	used, total := int64(10), int64(100)
	exp := t0.Add(30 * 24 * time.Hour)
	f := links("DE")
	f.Title = "Мой провайдер"
	f.Info = &subs.UserInfo{Used: &used, Total: &total, Expire: &exp}
	if _, _, err := p.ApplyFetch(sub.ID, f, false, t0); err != nil {
		t.Fatal(err)
	}
	got := p.Sub("s1")
	if got.Name != "Мой провайдер" || got.AutoName || *got.UsedBytes != 10 || *got.TotalBytes != 100 || !got.ExpireAt.Equal(exp) {
		t.Errorf("данные панели: %+v", got)
	}
	// The servers keep the prefix the subscription was born with.
	if ids(p)[0] != "s1-DE" {
		t.Errorf("приставка изменилась вместе с названием: %v", ids(p))
	}
	// A name the user chose is not overwritten by the panel.
	p.RenameSub("s1", "Основной")
	f.Title = "Другое"
	p.ApplyFetch("s1", f, false, t0)
	if p.Sub("s1").Name != "Основной" {
		t.Errorf("название пользователя затёрто: %q", p.Sub("s1").Name)
	}
}

func TestSelectionFavoritesAndRemoval(t *testing.T) {
	p := New()
	mustAdd(t, p, "https://a.example.com/1", "a", links("DE", "NL"))
	mustAdd(t, p, "https://b.example.com/1", "b", links("SE"))

	if lost := p.FixSelection(); lost != "" || p.Selected != "a-DE" {
		t.Errorf("первый выбор: %q, потерян %q", p.Selected, lost)
	}
	if err := p.Select("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("выбор несуществующего: %v", err)
	}
	p.Select("b-SE")
	p.SetFavorite("a-NL", true)
	p.SetFavorite("b-SE", true)
	p.SetFavorite("a-NL", true) // twice is once
	if !slices.Equal(p.Favorites, []string{"a-NL", "b-SE"}) {
		t.Fatalf("избранное: %v", p.Favorites)
	}
	if err := p.Reorder([]string{"b-SE"}); err != nil || !slices.Equal(p.Favorites, []string{"b-SE", "a-NL"}) {
		t.Errorf("порядок: %v, %v", p.Favorites, err)
	}
	if p.FavoriteOrder("a-NL") != 1 || p.FavoriteOrder("a-DE") != -1 {
		t.Errorf("места в избранном: NL=%d DE=%d", p.FavoriteOrder("a-NL"), p.FavoriteOrder("a-DE"))
	}

	// Removing the subscription of the selected server moves the selection
	// to the first favourite that is still there.
	if err := p.RemoveSub("s2"); err != nil {
		t.Fatal(err)
	}
	if lost := p.FixSelection(); lost != "b-SE" || p.Selected != "a-NL" {
		t.Errorf("после удаления подписки выбран %q, потерян %q", p.Selected, lost)
	}
	if !slices.Equal(p.Favorites, []string{"a-NL"}) {
		t.Errorf("избранное удалённой подписки осталось: %v", p.Favorites)
	}
	p.RemoveSub("s1")
	if p.FixSelection(); p.Selected != "" || len(p.Servers) != 0 {
		t.Errorf("пустой профиль: выбран %q, серверов %d", p.Selected, len(p.Servers))
	}
}

func TestAddLinkAndRemoveServer(t *testing.T) {
	p := New()
	mustAdd(t, p, "https://a.example.com/1", "a", links("DE"))
	var inv *Invalid
	if _, err := p.AddLink("просто текст", false); !errors.As(err, &inv) {
		t.Errorf("не ссылка: %v", err)
	}
	if _, err := p.AddLink("vmess://abc", false); !errors.As(err, &inv) || !strings.Contains(err.Error(), "не поддерживается") {
		t.Errorf("неподдерживаемый протокол: %v", err)
	}
	s, err := p.AddLink(" trojan://pw@my.example.com:443#Home ", false)
	if err != nil || s.ID != "Home" || s.Sub != "" || s.Protocol != "trojan" {
		t.Fatalf("сервер по ссылке: %+v, %v", s, err)
	}
	if err := p.RemoveServer("a-DE"); !errors.As(err, &inv) {
		t.Errorf("сервер подписки удалять нельзя: %v", err)
	}
	if err := p.RemoveServer("Home"); err != nil || p.Server("Home") != nil {
		t.Errorf("удаление своего сервера: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if p, err := Load(path); err != nil || p.Mode != proto.ModeRule || p.Servers == nil {
		t.Fatalf("нет файла: %+v, %v", p, err)
	}
	p := New()
	mustAdd(t, p, "https://a.example.com/1", "a", links("DE", "NL"))
	p.Select("a-NL")
	p.Mode = proto.ModeGlobal
	hwid := p.EnsureHWID()
	if !ValidHWID(hwid) || p.EnsureHWID() != hwid {
		t.Fatalf("HWID %q меняется или негоден", hwid)
	}
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Selected != "a-NL" || got.Mode != proto.ModeGlobal || got.HWID != hwid || got.NextSub != 2 ||
		!SameServers(got.Servers, p.Servers) {
		t.Errorf("после записи и чтения: %+v", got)
	}
	clone := p.Clone()
	clone.Servers[0].ID = "changed"
	if p.Servers[0].ID == "changed" {
		t.Error("Clone делит память с оригиналом")
	}
}

func TestDueForAutoUpdate(t *testing.T) {
	p := New()
	if p.Due(t0, 12*time.Hour) {
		t.Error("без подписок обновлять нечего")
	}
	sub, _ := p.AddSub("https://a.example.com/1", "a")
	if !p.Due(t0, 12*time.Hour) {
		t.Error("подписка, которую ни разу не качали, должна обновиться сразу")
	}
	if p.Due(t0, 0) {
		t.Error("автообновление выключено")
	}
	p.ApplyFetch(sub.ID, links("DE"), false, t0)
	if p.Due(t0.Add(11*time.Hour), 12*time.Hour) || !p.Due(t0.Add(12*time.Hour), 12*time.Hour) {
		t.Error("после удачного обновления следующее — через интервал")
	}
	// After a failure the next try comes sooner, but not at once.
	failed := t0.Add(12 * time.Hour)
	p.FetchFailed(sub.ID, errors.New("x"), failed)
	if p.Due(failed.Add(29*time.Minute), 12*time.Hour) || !p.Due(failed.Add(30*time.Minute), 12*time.Hour) {
		t.Error("после провала повтор через полчаса")
	}
}

func TestMaskURL(t *testing.T) {
	cases := map[string]string{
		"https://sub.example.com/api/sub/AbCdEf0123456789a3f9": "https://sub.example.com/…a3f9",
		"https://sub.example.com/short":                        "https://sub.example.com/…",
		"https://sub.example.com/x?token=SECRETSECRET1234":     "https://sub.example.com/…1234",
		"not a url": "…",
	}
	for in, want := range cases {
		if got := MaskURL(in); got != want {
			t.Errorf("%s: %q, ожидал %q", in, got, want)
		}
		if got := MaskURL(in); strings.Contains(got, "SECRET") || strings.Contains(got, "AbCdEf") {
			t.Errorf("%s: ключ виден в %q", in, got)
		}
	}
}

func TestAddSubKeepsTheAddressInsideAnAppLink(t *testing.T) {
	p := New()
	a, err := p.AddSub("https://panel.example.com/redirect?link=happ://add/https://panel.example.com/sub/abc123", "")
	if err != nil || a.URL != "https://panel.example.com/sub/abc123" || a.Name != "panel.example.com" || !a.AutoName {
		t.Fatalf("переадресация вокруг Happ: %+v, %v", a, err)
	}
	// The same address in another wrapper, or bare, is the same subscription.
	for _, again := range []string{"happ://add/https://panel.example.com/sub/abc123", "https://panel.example.com/sub/abc123"} {
		if _, err := p.AddSub(again, ""); !errors.Is(err, ErrExists) {
			t.Errorf("%q второй раз: %v", again, err)
		}
	}
	// The wrapper's name stands in until the panel names the subscription.
	b, err := p.AddSub("hiddify://import/https://other.example.com/sub/x#Мой VPN", "")
	if err != nil || b.URL != "https://other.example.com/sub/x" || b.Name != "Мой VPN" || !b.AutoName || b.Prefix != b.ID {
		t.Fatalf("имя из обёртки: %+v, %v", b, err)
	}
	if _, _, err := p.ApplyFetch(b.ID, subs.Fetched{Links: links("DE").Links, Title: "Провайдер"}, false, t0); err != nil {
		t.Fatal(err)
	}
	if b = p.Sub(b.ID); b.Name != "Провайдер" || b.AutoName {
		t.Errorf("название панели не заменило имя из обёртки: %+v", b)
	}
	// The user's own name beats the wrapper's.
	c, _ := p.AddSub("clash://install-config?url=https%3A%2F%2Fthird.example.com%2Fs&name=Wrapper", "lider")
	if c.Name != "lider" || c.AutoName || c.Prefix != "lider" {
		t.Errorf("своё имя: %+v", c)
	}
	var inv *Invalid
	if _, err := p.AddSub("happ://crypt5/QUJDREVG", ""); !errors.As(err, &inv) || !strings.Contains(inv.Text, "Happ") {
		t.Errorf("зашифрованная ссылка: %v", err)
	}
	if len(p.Subs) != 3 {
		t.Errorf("подписок %d, а должно быть 3", len(p.Subs))
	}
}

func TestMoveSubFollowsTheRedirect(t *testing.T) {
	p := New()
	a, _ := p.AddSub("https://go.example.com/r/abc", "")
	b, _ := p.AddSub("https://panel.example.com/sub/b", "mine")
	if err := p.MoveSub(a.ID, "https://panel.example.com/sub/a"); err != nil {
		t.Fatal(err)
	}
	if a = p.Sub(a.ID); a.URL != "https://panel.example.com/sub/a" || a.Name != "panel.example.com" || !a.AutoName {
		t.Errorf("после переадресации: %+v", a)
	}
	if err := p.MoveSub(a.ID, b.URL); !errors.Is(err, ErrExists) {
		t.Errorf("адрес другой подписки: %v", err)
	}
	if err := p.MoveSub(b.ID, "https://panel.example.com/sub/b2"); err != nil || p.Sub(b.ID).Name != "mine" {
		t.Errorf("своё имя не должно меняться: %+v, %v", p.Sub(b.ID), err)
	}
}
