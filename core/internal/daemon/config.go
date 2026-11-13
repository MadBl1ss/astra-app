package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// patchConfig puts this run's Clash API address and secret into a ready
// sing-box config and sends the engine's log to stderr, where the daemon
// reads it. Everything else in the config passes through untouched.
//
// The config is decoded into a generic tree, not a struct: a struct would
// silently drop every field this code does not know about.
func patchConfig(raw []byte, controller, secret string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	// Keep numbers as written; float64 would mangle large integers.
	dec.UseNumber()
	var cfg map[string]any
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config is not a JSON object: %w", err)
	}
	if cfg == nil {
		return nil, errors.New("config is null")
	}

	exp, err := child(cfg, "experimental")
	if err != nil {
		return nil, err
	}
	api, err := child(exp, "clash_api")
	if err != nil {
		return nil, err
	}
	api["external_controller"] = controller
	api["secret"] = secret

	log, err := child(cfg, "log")
	if err != nil {
		return nil, err
	}
	// With "output" set sing-box writes to that file and nothing to the
	// console, and the daemon would be blind to why a start failed.
	delete(log, "output")

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// child returns the object at key, creating it if absent.
func child(parent map[string]any, key string) (map[string]any, error) {
	switch v := parent[key].(type) {
	case nil:
		m := map[string]any{}
		parent[key] = m
		return m, nil
	case map[string]any:
		return v, nil
	default:
		return nil, fmt.Errorf("config: %q is not an object", key)
	}
}
