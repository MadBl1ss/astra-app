// Package diag packs what support needs into one archive: the state, the
// journal, the engines' logs and configs. Nothing that lets someone else use
// the user's servers may leave the machine in it: keys, passwords,
// subscription links and the device id are replaced before anything is
// written.
package diag

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"strelka/internal/store"
)

// Mask is what a secret is replaced with.
const Mask = "***"

// minSecret is the shortest value treated as a secret in free text. A short
// password replaced everywhere it occurs would garble the logs ("443" in
// every address), and a value that short is no key anyway.
const minSecret = 6

// secretKeys are the JSON fields whose values are credentials, in the
// spelling of sing-box (snake_case) and Xray (camelCase) alike: keys are
// compared lowercased with underscores dropped. "id" is Xray's name for the
// VLESS user id; sing-box has no field called that.
var secretKeys = map[string]bool{
	"uuid":         true,
	"id":           true,
	"password":     true,
	"privatekey":   true,
	"publickey":    true,
	"shortid":      true,
	"secret":       true,
	"auth":         true,
	"authstr":      true,
	"presharedkey": true,
	"token":        true,
}

func secretKey(key string) bool {
	return secretKeys[strings.ReplaceAll(strings.ToLower(key), "_", "")]
}

// uuidRe catches user ids that reach a log line without being known to the
// redactor, for instance from a server removed since.
var uuidRe = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

// Redactor removes known secrets from text.
type Redactor struct {
	secrets []string // longest first, so a key is not half-replaced by its own prefix
}

// NewRedactor keeps the secrets long enough to look for.
func NewRedactor(secrets []string) *Redactor {
	seen := map[string]bool{}
	var keep []string
	for _, s := range secrets {
		s = strings.TrimSpace(s)
		if len(s) < minSecret || seen[s] {
			continue
		}
		seen[s] = true
		keep = append(keep, s)
	}
	sort.Slice(keep, func(i, j int) bool { return len(keep[i]) > len(keep[j]) })
	return &Redactor{secrets: keep}
}

// Text replaces every known secret and anything shaped like a user id.
func (r *Redactor) Text(b []byte) []byte {
	for _, s := range r.secrets {
		b = bytes.ReplaceAll(b, []byte(s), []byte(Mask))
	}
	return uuidRe.ReplaceAll(b, []byte(Mask))
}

// Secrets walks a JSON document and returns the values of its credential
// fields, so that the same values can be found in free text later.
func Secrets(raw []byte) []string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var out []string
	walk(v, func(key string, value any) any {
		if s, ok := value.(string); ok && secretKey(key) {
			out = append(out, s)
		}
		return value
	})
	return out
}

// RedactJSON returns the document, indented, with every credential field
// masked. A file that is not JSON is not returned at all: there is no telling
// what is in it.
func RedactJSON(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	v = walk(v, func(key string, value any) any {
		if !secretKey(key) {
			return value
		}
		switch value.(type) {
		case string, float64:
			return Mask
		}
		return value
	})
	return json.MarshalIndent(v, "", "  ")
}

// walk visits every field of a decoded document; fn may replace the value.
func walk(v any, fn func(key string, value any) any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			t[k] = walk(fn(k, child), fn)
		}
	case []any:
		for i, child := range t {
			t[i] = walk(child, fn)
		}
	}
	return v
}

// Tail reads at most limit bytes from the end of a file, from the start of a
// line. A missing file is no error: an engine that never ran has no log.
func Tail(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	from := st.Size() - limit
	if from < 0 {
		from = 0
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, err
	}
	if from > 0 {
		if nl := bytes.IndexByte(data, '\n'); nl >= 0 {
			data = data[nl+1:]
		}
	}
	return data, nil
}

// File is one entry of the archive.
type File struct {
	Name string
	Data []byte
}

// Write packs files into a zip at path. The archive appears whole or not at
// all: it is built next to its final name and renamed into place.
func Write(path string, files []File, now time.Time) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return err
		}
		if _, err := w.Write(f.Data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return store.WriteAtomic(path, buf.Bytes(), 0o600)
}

// Prune keeps the newest keep archives in dir whose names start with prefix.
func Prune(dir, prefix string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".zip") {
			names = append(names, e.Name())
		}
	}
	// The names carry the time, so their order is their age.
	sort.Strings(names)
	for len(names) > keep {
		os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}
