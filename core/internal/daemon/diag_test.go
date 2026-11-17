package daemon

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"strelka/proto"
)

// reportProfile has everything that must not leave the machine: a device id,
// a subscription link with its token, a VLESS id and a Trojan password. All
// made up.
const reportProfile = `{
  "hwid": "hwid-made-up-0123456789",
  "subs": [{"id": "s1", "name": "Провайдер", "prefix": "p", "url": "https://sub.example.com/api/v1/client/tok0123456789abcdefXYZ?flag=meta"}],
  "servers": [
    {"id": "p-DE", "name": "Germany", "country": "DE", "protocol": "vless", "via": "sing-box", "host": "de.example.com", "port": 443,
     "outbound": {"type": "vless", "tag": "p-DE", "server": "de.example.com", "server_port": 443, "uuid": "11111111-1111-4111-8111-111111111111"}, "sub": "s1"},
    {"id": "p-NL", "name": "Netherlands", "country": "NL", "protocol": "trojan", "via": "sing-box", "host": "nl.example.com", "port": 443,
     "outbound": {"type": "trojan", "tag": "p-NL", "server": "nl.example.com", "server_port": 443, "password": "trojan-pass-made-up"}, "sub": "s1"}
  ],
  "selected": "p-DE",
  "mode": "rule",
  "favorites": [],
  "nextSub": 2
}`

func TestDiagExportPacksLogsWithoutKeys(t *testing.T) {
	paths := emptyData(t)
	if err := os.WriteFile(paths.ProfileFile(), []byte(reportProfile), 0o600); err != nil {
		t.Fatal(err)
	}
	// An engine log from an earlier run that quotes everything it should not.
	os.WriteFile(paths.EngineLog(boxName)+".1", []byte(
		"ERROR outbound/trojan[p-NL]: auth trojan-pass-made-up rejected\n"+
			"ERROR fetch https://sub.example.com/api/v1/client/tok0123456789abcdefXYZ?flag=meta: 403\n"+
			"INFO user 11111111-1111-4111-8111-111111111111 hwid hwid-made-up-0123456789\n"), 0o600)

	r := startRig(t, paths, "ok")
	r.call(proto.MethodVPNStart, nil, nil)
	r.waitPhase(proto.PhaseOn)
	// Where the user lives tells support nothing either.
	r.call(proto.MethodPlaceSet, proto.PlaceSetParams{Lat: 61.25, Lon: 73.42, City: "Выдумград"}, nil)

	var res proto.DiagExportResult
	if err := r.longCall(proto.MethodDiagExport, nil, &res); err != nil {
		t.Fatalf("diag.export: %v", err)
	}
	if filepath.Dir(res.Path) != paths.ReportsDir() || !strings.HasSuffix(res.Path, ".zip") {
		t.Fatalf("отчёт лёг не туда: %s", res.Path)
	}

	zr, err := zip.OpenReader(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(data)
	}
	for _, name := range []string{"README.txt", "about.txt", "state.json", "rules.json", "engines.json", "journal.jsonl", "logs/sing-box.log.1", "config/config.json"} {
		if got[name] == "" {
			t.Errorf("в отчёте нет %s; есть: %v", name, keys(got))
		}
	}
	secrets := []string{
		"trojan-pass-made-up",
		"11111111-1111-4111-8111-111111111111",
		"tok0123456789abcdefXYZ",
		"hwid-made-up-0123456789",
		"Выдумград",
		"73.42",
	}
	for name, data := range got {
		for _, s := range secrets {
			if strings.Contains(data, s) {
				t.Errorf("%s: в отчёт попало %q", name, s)
			}
		}
	}
	// The tags stay readable: without them the logs say nothing.
	if !strings.Contains(got["logs/sing-box.log.1"], "outbound/trojan[p-NL]") || !strings.Contains(got["config/config.json"], `"p-DE"`) {
		t.Errorf("теги серверов пропали:\n%s\n%s", got["logs/sing-box.log.1"], got["config/config.json"])
	}

	r.waitState("запись об отчёте в журнале", func(proto.State) bool {
		for _, e := range r.journal {
			if strings.Contains(e.Text, filepath.Base(res.Path)) {
				return true
			}
		}
		return false
	})
}

func TestEngineVersionsListsBothEngines(t *testing.T) {
	r := startRig(t, newData(t), "ok")
	var res proto.EngineVersionsResult
	r.call(proto.MethodEngineVersions, nil, &res)
	// The engines folder of a test is empty: both are named, neither known.
	if len(res.Engines) != 2 || res.Engines[0].Name != boxName || res.Engines[1].Name != xrayName ||
		res.Engines[0].Version != nil || res.Engines[1].Version != nil {
		t.Fatalf("engine.versions: %+v", res.Engines)
	}
}

func TestParseVersion(t *testing.T) {
	for out, want := range map[string]string{
		"sing-box version 1.14.0\n\nEnvironment: go1.25.1 windows/amd64\n":                          "1.14.0",
		"Xray 26.3.27 (Xray, Penetrates Everything.) 1a2b3c4 (go1.25.1 windows/amd64)\nA unified…\n": "26.3.27",
		"sing-box version 1.15.0-beta.2\n":                                                           "1.15.0-beta.2",
		"flag provided but not defined: -version\n":                                                  "",
	} {
		if got := parseVersion([]byte(out)); got != want {
			t.Errorf("parseVersion(%q) = %q, жду %q", out, got, want)
		}
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
