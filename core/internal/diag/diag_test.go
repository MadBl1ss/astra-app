package diag

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Made-up credentials of made-up servers.
const (
	userID   = "11111111-2222-4333-8444-555555555555"
	password = "correct-horse-battery"
	pubKey   = "Zm9vYmFyYmF6cXV4LXB1YmxpYy1rZXk"
)

func TestRedactJSONMasksCredentialsInBothSpellings(t *testing.T) {
	singBox := []byte(`{"outbounds":[{"type":"vless","tag":"de-1","server":"192.0.2.1","uuid":"` + userID + `",
		"tls":{"reality":{"public_key":"` + pubKey + `","short_id":"abcd"}}},
		{"type":"trojan","tag":"nl-1","password":"` + password + `"}],
		"experimental":{"clash_api":{"secret":"s3cr3t-value"}}}`)
	xray := []byte(`{"outbounds":[{"tag":"de-1","settings":{"vnext":[{"address":"192.0.2.1","users":[{"id":"` + userID + `"}]}]},
		"streamSettings":{"realitySettings":{"publicKey":"` + pubKey + `","shortId":"abcd"}}}]}`)

	for name, raw := range map[string][]byte{"sing-box": singBox, "xray": xray} {
		out, err := RedactJSON(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, secret := range []string{userID, password, pubKey, "s3cr3t-value", `"abcd"`} {
			if bytes.Contains(out, []byte(secret)) {
				t.Errorf("%s: в отчёт попало %q:\n%s", name, secret, out)
			}
		}
		// What support needs to read the config stays.
		for _, keep := range []string{"192.0.2.1", `"de-1"`} {
			if !bytes.Contains(out, []byte(keep)) {
				t.Errorf("%s: пропало %q:\n%s", name, keep, out)
			}
		}
	}
}

func TestRedactJSONRefusesWhatIsNotJSON(t *testing.T) {
	if _, err := RedactJSON([]byte("uuid=" + userID)); err == nil {
		t.Error("не-JSON прошёл: его содержимое нельзя проверить")
	}
}

func TestSecretsAreFoundAndRemovedFromText(t *testing.T) {
	outbound := []byte(`{"type":"trojan","tag":"nl-1","server":"nl1.example.com","password":"` + password + `"}`)
	secrets := append(Secrets(outbound), "https://sub.example.com/api/v1/client/abcdef123456", "short")
	r := NewRedactor(secrets)

	log := []byte("outbound/trojan[nl-1]: auth " + password + " to nl1.example.com\n" +
		"fetch https://sub.example.com/api/v1/client/abcdef123456 failed\n" +
		"vless user " + userID + " rejected\n" +
		"a short word stays\n")
	out := string(r.Text(log))
	for _, gone := range []string{password, "abcdef123456", userID} {
		if strings.Contains(out, gone) {
			t.Errorf("в тексте осталось %q:\n%s", gone, out)
		}
	}
	for _, kept := range []string{"nl1.example.com", "nl-1", "a short word stays"} {
		if !strings.Contains(out, kept) {
			t.Errorf("пропало %q:\n%s", kept, out)
		}
	}
}

func TestTailStartsAtALine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.log")
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("line number ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString("\n")
	}
	os.WriteFile(path, []byte(b.String()), 0o600)

	got, err := Tail(path, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 200 || !strings.HasPrefix(string(got), "line number") {
		t.Errorf("хвост не с начала строки или длиннее предела: %d байт, %q", len(got), got[:20])
	}
	if !strings.HasSuffix(b.String(), string(got)) {
		t.Error("хвост — не конец файла")
	}

	if got, err := Tail(filepath.Join(t.TempDir(), "missing.log"), 200); err != nil || got != nil {
		t.Errorf("нет файла: %q, %v; жду пусто без ошибки", got, err)
	}
}

func TestWriteAndPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		name := filepath.Join(dir, "strelka-report-2026111"+string(rune('0'+i))+".zip")
		if err := Write(name, []File{{Name: "state.json", Data: []byte(`{}`)}, {Name: "logs/sing-box.log", Data: []byte("hi\n")}}, now); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "other.zip"), nil, 0o600)
	Prune(dir, "strelka-report-", 5)

	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 6 || names[0] != "other.zip" || names[1] != "strelka-report-20261112.zip" {
		t.Fatalf("после чистки: %v; жду пять свежих отчётов и чужой файл", names)
	}

	zr, err := zip.OpenReader(filepath.Join(dir, "strelka-report-20261116.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != 2 || zr.File[1].Name != "logs/sing-box.log" {
		t.Fatalf("в архиве: %+v", zr.File)
	}
	rc, _ := zr.File[1].Open()
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "hi\n" {
		t.Errorf("содержимое: %q", data)
	}
}
