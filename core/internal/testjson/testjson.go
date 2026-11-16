// Package testjson is a tiny path lookup for tests that inspect generated
// JSON. It exists so the module does not take a dependency for it.
package testjson

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Get walks raw along a dotted path ("outbounds.0.tag") and returns the
// value as a string; a missing path is "". Objects and arrays come back as
// compact JSON.
func Get(raw []byte, path string) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	for _, key := range strings.Split(path, ".") {
		switch node := v.(type) {
		case map[string]any:
			v = node[key]
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(node) {
				return ""
			}
			v = node[i]
		default:
			return ""
		}
	}
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// Find returns the first element of the array at path whose field equals
// want, as JSON, or nil.
func Find(raw []byte, path, field, want string) []byte {
	var arr []json.RawMessage
	if json.Unmarshal([]byte(Get(raw, path)), &arr) != nil {
		return nil
	}
	for _, el := range arr {
		if Get(el, field) == want {
			return el
		}
	}
	return nil
}
