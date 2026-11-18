package rules

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"strelka/internal/testjson"
	"strelka/proto"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ kind, in, wantKind, want string }{
		{"", "steam.exe", "process", "steam.exe"},
		{"", `C:\Program Files (x86)\Steam\steam.exe`, "process", "steam.exe"},
		{"", "https://Store.SteamPowered.com/app/570/", "suffix", "store.steampowered.com"},
		{"", "*.youtube.com", "suffix", "youtube.com"},
		{"", "https://www.Example.org/some/path", "suffix", "example.org"},
		{"suffix", "www.com", "suffix", "www.com"},
		{"", ".googlevideo.com", "suffix", "googlevideo.com"},
		{"", "91.108.4.1", "ip", "91.108.4.1/32"},
		{"", "91.108.4.7/22", "ip", "91.108.4.0/22"},
		{"", "2001:db8::1", "ip", "2001:db8::1/128"},
		{"process", "Discord", "process", "Discord.exe"},
		{"keyword", "Telegram", "keyword", "telegram"},
		{"domain", "Example.com", "domain", "example.com"},
	}
	for _, c := range cases {
		k, v, err := normalize(c.kind, c.in, true)
		if err != nil || k != c.wantKind || v != c.want {
			t.Errorf("normalize(%q, %q) = %q, %q, %v; ожидал %q, %q", c.kind, c.in, k, v, err, c.wantKind, c.want)
		}
	}
	for _, bad := range []struct{ kind, in string }{
		{"", ""}, {"suffix", "localhost"}, {"ip", "300.1.1.1"}, {"process", "a:b.exe"}, {"nope", "x"}, {"", "просто слово"},
	} {
		_, _, err := normalize(bad.kind, bad.in, true)
		var inv *Invalid
		if err == nil || !asInvalid(err, &inv) {
			t.Errorf("normalize(%q, %q) должен был отказать с текстом для пользователя, а вернул %v", bad.kind, bad.in, err)
		}
	}

	// On Linux a program has no .exe and is given by path or bare name.
	if k, v, err := normalize("", "/usr/bin/curl", false); err != nil || k != "process" || v != "curl" {
		t.Errorf("путь на Linux: %q %q %v", k, v, err)
	}
	if _, v, _ := normalize("process", "firefox", false); v != "firefox" {
		t.Errorf("на Linux к имени программы дописано расширение: %q", v)
	}
}

func asInvalid(err error, target **Invalid) bool {
	inv, ok := err.(*Invalid)
	*target = inv
	return ok
}

func TestRoundTripAndMove(t *testing.T) {
	dir := t.TempDir()
	var s Set
	s.Add(TagVPN, "process", "steamwebhelper.exe")
	s.Add(TagVPN, "suffix", "steamcommunity.com")
	s.Add(TagVPN, "domain", "exact.example.com")
	s.Add(TagVPN, "ip", "91.108.0.0/16")
	s.Add(TagDirect, "process", "steam.exe")
	// Adding to the other side moves it.
	s.Add(TagDirect, "suffix", "steamcommunity.com")
	if slices.Contains(s.VPN.Suffixes, "steamcommunity.com") || !slices.Contains(s.Direct.Suffixes, "steamcommunity.com") {
		t.Fatalf("домен не переехал: %+v", s)
	}
	s.Add(TagVPN, "suffix", "steamcommunity.com")

	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.VPN.Processes, []string{"steamwebhelper.exe"}) ||
		!slices.Equal(got.VPN.Suffixes, []string{"steamcommunity.com"}) ||
		!slices.Equal(got.VPN.Domains, []string{"exact.example.com"}) ||
		!slices.Equal(got.VPN.IPs, []string{"91.108.0.0/16"}) ||
		!slices.Equal(got.Direct.Processes, []string{"steam.exe"}) ||
		len(got.Direct.Suffixes) != 0 {
		t.Fatalf("после сохранения и чтения: %+v", got)
	}
	if got.Count() != 5 {
		t.Errorf("правил %d, ожидал 5", got.Count())
	}

	// Programs and domains must be separate headless rules: in one rule
	// sing-box ANDs them, so nothing would ever match.
	raw, _ := os.ReadFile(File(dir, TagVPN))
	if testjson.Get(raw, "rules.0.process_name.0") != "steamwebhelper.exe" || testjson.Get(raw, "rules.0.domain_suffix") != "" {
		t.Fatalf("process_name и domain_suffix в одном правиле: %s", raw)
	}
	// The DNS file must never carry IPs or programs (see the package comment).
	dns, _ := os.ReadFile(File(dir, TagVPN+"-dns"))
	if strings.Contains(string(dns), "ip_cidr") || strings.Contains(string(dns), "process_name") ||
		!strings.Contains(string(dns), "steamcommunity.com") || !strings.Contains(string(dns), "exact.example.com") {
		t.Errorf("vpn-dns.json: %s", dns)
	}

	if !got.Remove(TagVPN, "process", "steamwebhelper.exe") || len(got.VPN.Processes) != 0 {
		t.Fatalf("не удалилось: %+v", got.VPN.Processes)
	}
	if got.Remove(TagVPN, "process", "steamwebhelper.exe") || got.Remove(TagVPN, "nope", "x") {
		t.Error("удаление того, чего нет, должно вернуть false")
	}
}

// sing-box reloads a rule-set when its file changes, so a save that changes
// nothing must not touch the file.
func TestSaveLeavesUnchangedFilesAlone(t *testing.T) {
	dir := t.TempDir()
	var s Set
	s.Add(TagVPN, "process", "a.exe")
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	stamp := func(tag string) int64 {
		fi, err := os.Stat(File(dir, tag))
		if err != nil {
			t.Fatal(err)
		}
		return fi.ModTime().UnixNano()
	}
	old := map[string]int64{}
	for _, tag := range []string{TagVPN, TagVPN + "-dns", TagDirect, TagDirect + "-dns"} {
		// Push the times into the past: file systems round them, and a
		// rewrite within the same tick would go unnoticed.
		past := stampPast(t, File(dir, tag))
		old[tag] = past
	}
	s.Add(TagVPN, "process", "b.exe")
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	if stamp(TagVPN) == old[TagVPN] {
		t.Error("vpn.json изменился, а файл не переписан")
	}
	for _, tag := range []string{TagVPN + "-dns", TagDirect, TagDirect + "-dns"} {
		if stamp(tag) != old[tag] {
			t.Errorf("%s.json не менялся, а файл переписан", tag)
		}
	}
}

func TestEnsureStartsFromBuiltInLists(t *testing.T) {
	dir := t.TempDir() + "/rules"
	s, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.VPN.Processes, "Discord.exe") || !slices.Contains(s.VPN.Suffixes, "discord.com") ||
		!slices.Contains(s.Direct.Processes, "steam.exe") {
		t.Fatalf("встроенные списки: %+v", s)
	}
	for _, tag := range []string{TagVPN, TagVPN + "-dns", TagDirect, TagDirect + "-dns", TagServersDNS} {
		if _, err := os.Stat(File(dir, tag)); err != nil {
			t.Errorf("нет файла %s.json: %v", tag, err)
		}
	}

	// The user emptied the VPN list. That is a choice, not a first run: the
	// samples must not come back.
	s.VPN = List{}
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	again, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.VPN.Processes) != 0 || !slices.Contains(again.Direct.Processes, "steam.exe") {
		t.Errorf("после очистки списка образцы вернулись: %+v", again.VPN)
	}
}

// What the built-in lists gain later reaches an existing install once: not
// again after the user has removed it, and not over the user's own choice
// of the other list.
func TestAdditionsReachAnOldInstallOnce(t *testing.T) {
	for _, a := range additions {
		d := Defaults()
		if !slices.Contains(*field(d.list(a.list), a.kind), a.value) {
			t.Errorf("%s нет среди встроенных: у новой установки его не будет", a.value)
		}
	}

	// A folder from before the additions, with the user's own lists and no
	// mark of what it has taken.
	dir := t.TempDir() + "/rules"
	old := Set{}
	old.Direct.Processes = []string{"steam.exe"}
	old.VPN.Processes = []string{"exitlag.exe"} // the user wants this one through the VPN
	if err := Save(dir, old); err != nil {
		t.Fatal(err)
	}
	s, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.Direct.Processes, " ") != "ExitLag.exe steam.exe" || strings.Join(s.VPN.Processes, " ") != "exitlag.exe" {
		t.Fatalf("после обновления: direct=%v vpn=%v", s.Direct.Processes, s.VPN.Processes)
	}

	// The user removes it: the next start does not bring it back.
	s.Direct.Processes = []string{"steam.exe"}
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	again, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(again.Direct.Processes, " ") != "steam.exe" {
		t.Errorf("удалённое вернулось: %v", again.Direct.Processes)
	}

	// A fresh install has them already and takes nothing on top later.
	fresh := t.TempDir() + "/rules"
	first, err := Ensure(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if readTaken(fresh) != len(additions) || !slices.Contains(first.Direct.Processes, "ExitLag.exe") {
		t.Errorf("новая установка: взято %d из %d, direct=%v", readTaken(fresh), len(additions), first.Direct.Processes)
	}
}

func TestWriteServersDNS(t *testing.T) {
	dir := t.TempDir()
	if err := WriteServersDNS(dir, []string{"NL.example.com", "192.0.2.7", "de.example.com", "nl.example.com", "2001:db8::1", ""}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(File(dir, TagServersDNS))
	if got := testjson.Get(raw, "rules.0.domain"); got != `["de.example.com","nl.example.com"]` {
		t.Errorf("имена серверов: %s", got)
	}
	if err := WriteServersDNS(dir, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(File(dir, TagServersDNS))
	if testjson.Get(raw, "rules") != "[]" {
		t.Errorf("пустой список должен давать пустые правила: %s", raw)
	}
}

func TestMatch(t *testing.T) {
	var s Set
	s.Add(TagVPN, proto.KindProcess, "Discord.exe")
	s.Add(TagVPN, proto.KindSuffix, "example.com")
	s.Add(TagVPN, proto.KindDomain, "only.example.org")
	s.Add(TagVPN, proto.KindKeyword, "telegram")
	s.Add(TagVPN, proto.KindIP, "91.108.0.0/16")

	hits := []struct{ kind, value, ruleKind, rule string }{
		{"process", "Discord.exe", "process", "Discord.exe"},
		{"suffix", "example.com", "suffix", "example.com"},
		{"suffix", "cdn.eu.example.com", "suffix", "example.com"},
		{"suffix", "only.example.org", "domain", "only.example.org"},
		{"suffix", "web.telegram.org", "keyword", "telegram"},
		{"ip", "91.108.4.1/32", "ip", "91.108.0.0/16"},
		{"ip", "91.108.4.0/22", "ip", "91.108.0.0/16"},
	}
	for _, c := range hits {
		rk, r, ok := Match(s.VPN, c.kind, c.value)
		if !ok || rk != c.ruleKind || r != c.rule {
			t.Errorf("Match(%s %s) = %q %q %v; ожидал %q %q", c.kind, c.value, rk, r, ok, c.ruleKind, c.rule)
		}
	}
	misses := []struct{ kind, value string }{
		{"process", "discord.exe"},         // process names match exactly
		{"suffix", "notexample.com"},       // a suffix ends at a dot
		{"suffix", "sub.only.example.org"}, // an exact name covers no subdomains
		{"ip", "91.109.0.1/32"},
		{"ip", "91.0.0.0/8"}, // wider than the rule: only part of it is covered
	}
	for _, c := range misses {
		if rk, r, ok := Match(s.VPN, c.kind, c.value); ok {
			t.Errorf("Match(%s %s) не должен был сработать, а нашёл %s %s", c.kind, c.value, rk, r)
		}
	}
}

// stampPast moves a file's modification time an hour back and returns it.
func stampPast(t *testing.T, path string) int64 {
	t.Helper()
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.ModTime().UnixNano()
}
