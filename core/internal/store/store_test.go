package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type sample struct {
	Desired string   `json:"desired"`
	Port    int      `json:"port"`
	Tags    []string `json:"tags"`
}

func TestSaveAndLoadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	want := sample{Desired: "on", Port: 20808, Tags: []string{"a"}}
	if err := SaveJSON(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadJSON(path, sample{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Desired != "on" || got.Port != 20808 || len(got.Tags) != 1 {
		t.Errorf("прочитано %+v", got)
	}
}

func TestLoadJSONDefaults(t *testing.T) {
	dir := t.TempDir()
	def := sample{Desired: "off", Port: 7890}

	got, err := LoadJSON(filepath.Join(dir, "missing.json"), def)
	if err != nil || got.Desired != "off" || got.Port != 7890 {
		t.Errorf("нет файла: %+v, %v — жду значения по умолчанию без ошибки", got, err)
	}

	partial := filepath.Join(dir, "partial.json")
	os.WriteFile(partial, []byte(`{"desired":"on"}`), 0o600)
	got, err = LoadJSON(partial, def)
	if err != nil || got.Desired != "on" || got.Port != 7890 {
		t.Errorf("неполный файл: %+v, %v — жду порт по умолчанию", got, err)
	}

	broken := filepath.Join(dir, "broken.json")
	os.WriteFile(broken, []byte(`{"desired":"on","port":`), 0o600)
	got, err = LoadJSON(broken, def)
	if err == nil {
		t.Error("битый файл должен дать ошибку")
	}
	if got.Desired != "off" || got.Port != 7890 {
		t.Errorf("битый файл испортил значения по умолчанию: %+v", got)
	}
}

func TestWriteAtomicReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	for _, body := range []string{"first", "second"} {
		if err := WriteAtomic(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "second" {
		t.Errorf("в файле %q", raw)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("остался временный файл %s", e.Name())
		}
	}
	if err := WriteAtomic(filepath.Join(dir, "nope", "x.json"), []byte("x"), 0o600); err == nil {
		t.Error("запись в несуществующую папку должна дать ошибку")
	}
}

func TestPaths(t *testing.T) {
	p := Paths{Data: t.TempDir(), Engines: filepath.Join("C:", "engines")}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{p.RunDir(), p.LogsDir()} {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Errorf("папка %s не создана", dir)
		}
	}
	if filepath.Dir(p.RunConfig()) != p.RunDir() {
		t.Errorf("run/config.json лежит не в run: %s", p.RunConfig())
	}
	if !strings.HasPrefix(filepath.Base(p.Engine("sing-box")), "sing-box") || filepath.Dir(p.Engine("sing-box")) != p.Engines {
		t.Errorf("путь движка: %s", p.Engine("sing-box"))
	}
}
