package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func TestDefaultPolicyPausesGrowAndEndInCrashLoop(t *testing.T) {
	r := newRestarts(DefaultPolicy())
	now := t0
	want := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}
	for i, w := range want {
		d, loop := r.crashed(now, 200*time.Millisecond)
		if loop || d != w {
			t.Fatalf("падение %d: пауза %v, crashloop=%v — жду %v", i+1, d, loop, w)
		}
		now = now.Add(d + 200*time.Millisecond)
	}
	// 1+2+5+10 s of pauses fit in two minutes: the fifth crash is the limit.
	if _, loop := r.crashed(now, 200*time.Millisecond); !loop {
		t.Error("пятое падение за две минуты должно быть crashloop")
	}
}

func TestPauseResetsAfterAStableRun(t *testing.T) {
	r := newRestarts(DefaultPolicy())
	now := t0
	r.crashed(now, time.Second)
	now = now.Add(3 * time.Second)
	if d, _ := r.crashed(now, time.Second); d != 2*time.Second {
		t.Fatalf("вторая пауза %v", d)
	}
	// It then ran for an hour: the next crash is a new story.
	now = now.Add(time.Hour)
	d, loop := r.crashed(now, time.Hour)
	if loop || d != time.Second {
		t.Errorf("после часа работы пауза %v, crashloop=%v — жду 1 с", d, loop)
	}
	if r.count() != 1 {
		t.Errorf("старые падения должны выйти из окна, в счёте %d", r.count())
	}
}

func TestSlowCrashesNeverBecomeACrashLoop(t *testing.T) {
	r := newRestarts(DefaultPolicy())
	now := t0
	// Dies every 40 s: never stable, never five within two minutes.
	for i := 0; i < 20; i++ {
		d, loop := r.crashed(now, 40*time.Second)
		if loop {
			t.Fatalf("падение %d: редкие падения приняты за crashloop", i+1)
		}
		if i >= 4 && d != 30*time.Second {
			t.Fatalf("падение %d: пауза %v, жду потолок 30 с", i+1, d)
		}
		now = now.Add(d + 40*time.Second)
	}
}

func TestRingKeepsTheLastLines(t *testing.T) {
	r := newRing(3)
	if got := r.tail(5); len(got) != 0 {
		t.Errorf("пустое кольцо: %v", got)
	}
	for _, s := range []string{"a", "b"} {
		r.add(s)
	}
	if got := strings.Join(r.tail(5), ""); got != "ab" {
		t.Errorf("неполное кольцо: %q", got)
	}
	for _, s := range []string{"c", "d", "e"} {
		r.add(s)
	}
	if got := strings.Join(r.tail(5), ""); got != "cde" {
		t.Errorf("после переполнения: %q, жду cde", got)
	}
	if got := strings.Join(r.tail(2), ""); got != "de" {
		t.Errorf("tail(2): %q", got)
	}
}

func TestReadLinesCleansEngineOutput(t *testing.T) {
	in := "\x1b[36mINFO\x1b[0m[0000] sing-box started\r\n" +
		"\n" +
		strings.Repeat("x", 3*maxLineBytes) + "\n" +
		"last line without newline"
	var got []string
	readLines(strings.NewReader(in), func(s string) { got = append(got, s) })
	if len(got) != 3 {
		t.Fatalf("строк %d, жду 3: %q", len(got), got)
	}
	if got[0] != "INFO[0000] sing-box started" {
		t.Errorf("цвета и \\r не убраны: %q", got[0])
	}
	if len(got[1]) > maxLineBytes {
		t.Errorf("длинная строка не обрезана: %d байт", len(got[1]))
	}
	if got[2] != "last line without newline" {
		t.Errorf("последняя строка: %q", got[2])
	}
}

func TestLevel(t *testing.T) {
	for line, want := range map[string]string{
		"+0300 2026-11-12 10:00:00 INFO router: started":           "info",
		"+0300 2026-11-12 10:00:00 ERROR inbound/mixed: boom":      "error",
		"FATAL[0000] start service: listen tcp: bind: denied":      "error",
		"WARN[0010] dns: exchange failed":                          "warn",
		"2026/11/12 10:00:00 [Warning] core: Xray 26.3.27 started": "warn",
		"DEBUG[0003] connection: closed":                           "debug",
		"something without a level":                                "info",
	} {
		if got := Level(line); got != want {
			t.Errorf("%q: %s, жду %s", line, got, want)
		}
	}
}

func TestRotatingFileKeepsOnePreviousFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.log")
	rf := openRotating(path, 100)
	line := strings.Repeat("a", 39) // 40 bytes with the newline
	for i := 0; i < 5; i++ {
		rf.writeLine(line)
	}
	rf.close()
	cur, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("старый файл не сохранён: %v", err)
	}
	if len(cur) > 100 || len(old) > 100 {
		t.Errorf("размеры %d и %d, предел 100", len(cur), len(old))
	}
	if len(cur) == 0 {
		t.Error("после ротации в новый файл ничего не записано")
	}

	// Reopening appends rather than truncates: a daemon restart keeps logs.
	rf = openRotating(path, 100)
	rf.writeLine("b")
	rf.close()
	again, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(again), string(cur)) || !strings.HasSuffix(string(again), "b\n") {
		t.Errorf("повторное открытие затёрло файл: %q", again)
	}
}

func TestFreePortAndSecret(t *testing.T) {
	p, err := FreePort()
	if err != nil || p <= 0 || p > 65535 {
		t.Fatalf("порт %d, %v", p, err)
	}
	a, _ := NewSecret()
	b, _ := NewSecret()
	if len(a) != 32 || a == b {
		t.Errorf("секреты %q и %q: жду 16 случайных байт в hex", a, b)
	}
}
