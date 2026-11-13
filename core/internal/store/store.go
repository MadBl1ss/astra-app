// Package store knows where the daemon keeps its files and how to write them
// so that a crash or power loss never leaves a half-written one.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// Paths are the two roots the daemon works with: its own data and the folder
// the engines are started from.
type Paths struct {
	Data    string
	Engines string
}

func (p Paths) RunDir() string      { return filepath.Join(p.Data, "run") }
func (p Paths) RunConfig() string   { return filepath.Join(p.Data, "run", "config.json") }
func (p Paths) LogsDir() string     { return filepath.Join(p.Data, "logs") }
func (p Paths) Journal() string     { return filepath.Join(p.Data, "journal.jsonl") }
func (p Paths) DesiredFile() string { return filepath.Join(p.Data, "desired.json") }

// EngineLog is where an engine's stderr is kept, e.g. logs/sing-box.log.
func (p Paths) EngineLog(name string) string { return filepath.Join(p.Data, "logs", name+".log") }

// Engine is the full path of an engine binary. Engines are only ever started
// from this folder, and stray processes are matched against this exact path.
func (p Paths) Engine(name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(p.Engines, name)
}

// Ensure creates the data folders. The data dir holds subscription keys, so
// it is created private to the owner where the OS honours mode bits.
func (p Paths) Ensure() error {
	for _, dir := range []string{p.Data, p.RunDir(), p.LogsDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// WriteAtomic replaces path with data in one step: readers see either the old
// file or the new one. The temp file lives in the same folder because a
// rename across volumes is a copy, not a rename.
func WriteAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}
	if _, err := f.Write(data); err != nil {
		cleanup()
		return err
	}
	// Without the sync a rename can land on disk before the data does.
	if err := f.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// SaveJSON writes v as indented JSON, atomically.
func SaveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(path, append(data, '\n'), 0o600)
}

// LoadJSON reads path over def: fields the file does not mention keep their
// default. A missing file is not an error. A broken file is: the caller gets
// def back together with the error and decides whether to carry on.
func LoadJSON[T any](path string, def T) (T, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	// Decode into a copy made through JSON, so a failure halfway cannot leave
	// def's maps and slices half-overwritten.
	v, err := clone(def)
	if err != nil {
		return def, err
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return def, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return v, nil
}

func clone[T any](v T) (T, error) {
	var out T
	raw, err := json.Marshal(v)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}
