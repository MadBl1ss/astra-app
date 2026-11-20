package proto

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The same files feed the mock daemon of the app, so a field that Go drops or
// renames here would silently diverge the two sides.
var fixtureNames = []string{"ok", "off", "starting", "server_down", "blocked", "no_internet", "empty", "testing"}

func TestFixturesRoundTrip(t *testing.T) {
	dir := filepath.Join("testdata", "fixtures")
	for _, name := range fixtureNames {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var st State
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&st); err != nil {
				t.Fatalf("образец не разбирается в State: %v", err)
			}
			out, err := json.Marshal(st)
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Errorf("образец изменился после разбора и сборки:\n было  %s\n стало %s", compact(raw), out)
			}
			if st.Proto != Version {
				t.Errorf("proto в образце %d, в коде %d", st.Proto, Version)
			}
		})
	}
}

func TestFixturesCoverTheDirectory(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "fixtures", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(fixtureNames) {
		t.Errorf("в testdata/fixtures %d файлов, тест знает %d: добавь новый образец в fixtureNames", len(files), len(fixtureNames))
	}
}

// A fresh daemon must already produce a document clients can index blindly.
func TestNewStateHasNoNullArrays(t *testing.T) {
	out, err := json.Marshal(NewState("0.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"servers", "subs", "incidents"} {
		if _, ok := m[k].([]any); !ok {
			t.Errorf("%s должен быть массивом, а не %T", k, m[k])
		}
	}
	if m["busy"] != nil {
		t.Errorf("busy у новой службы: %v", m["busy"])
	}
}

func TestMessageKinds(t *testing.T) {
	var m Message
	if err := json.Unmarshal([]byte(`{"jsonrpc":"2.0","method":"state","params":{}}`), &m); err != nil {
		t.Fatal(err)
	}
	if !m.IsNotification() {
		t.Error("сообщение без id должно быть уведомлением")
	}
	m = Message{}
	if err := json.Unmarshal([]byte(`{"jsonrpc":"2.0","id":7,"method":"hello"}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.IsNotification() || string(m.ID) != "7" {
		t.Errorf("запрос разобран неверно: %+v", m)
	}
}

func compact(raw []byte) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}
