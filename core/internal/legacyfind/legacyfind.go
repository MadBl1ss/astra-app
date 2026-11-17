// Package legacyfind looks for an old Strelka on the machine, so that the
// first run of the new one can offer to carry its subscriptions over. It
// only ever looks: the old one may be the user's working VPN.
package legacyfind

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// TaskName is the scheduled task the old tray's setup registers to start it
// at logon.
const TaskName = "sing-box VPN autostart"

// Marker is the file that tells an old Strelka's folder from any other
// sing-box: its list of subscriptions.
const Marker = "subscription.txt"

// Candidate is a folder that may hold an old Strelka.
type Candidate struct {
	Dir string
	// Running: an old Strelka's program runs from this folder right now.
	Running bool
}

// Find returns the folder of an old Strelka; its Dir is "" when there is
// none. ours are folders of this Strelka (its engines, its data), skipped
// even if a marker were to appear there.
func Find(ctx context.Context, ours ...string) Candidate {
	return Pick(candidates(ctx), ours)
}

// Pick returns the first candidate that holds the marker. The order of the
// candidates is the order of preference.
func Pick(cands []Candidate, ours []string) Candidate {
	skip := map[string]bool{}
	for _, o := range ours {
		if o != "" {
			skip[key(o)] = true
		}
	}
	for _, c := range cands {
		if c.Dir == "" || !filepath.IsAbs(c.Dir) || skip[key(c.Dir)] {
			continue
		}
		fi, err := os.Stat(filepath.Join(c.Dir, Marker))
		if err == nil && fi.Mode().IsRegular() {
			return Candidate{Dir: filepath.Clean(c.Dir), Running: c.Running}
		}
	}
	return Candidate{}
}

// key makes two spellings of one folder equal. Windows does not tell case
// apart in paths; Linux does.
func key(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// TaskDirs reads a scheduled task, as `schtasks /query /xml` prints it, for
// the folders its actions point at, most telling first: the working folder,
// then the folders of files named in the arguments (the old setup starts a
// PowerShell script that lives next to the engine), then the program's own.
func TaskDirs(raw []byte) []string {
	var task struct {
		Exec []struct {
			Command          string `xml:"Command"`
			Arguments        string `xml:"Arguments"`
			WorkingDirectory string `xml:"WorkingDirectory"`
		} `xml:"Actions>Exec"`
	}
	dec := xml.NewDecoder(bytes.NewReader(toUTF8(raw)))
	// The declaration says UTF-16 whatever the bytes are; they are UTF-8 by
	// now, so the label is ignored.
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	if err := dec.Decode(&task); err != nil {
		return nil
	}
	var out []string
	for _, e := range task.Exec {
		if wd := expand(e.WorkingDirectory); wd != "" {
			out = append(out, wd)
		}
		for _, arg := range splitArgs(e.Arguments) {
			if p := expand(arg); filepath.IsAbs(p) {
				out = append(out, filepath.Dir(p))
			}
		}
		if p := expand(e.Command); filepath.IsAbs(p) {
			out = append(out, filepath.Dir(p))
		}
	}
	return out
}

func expand(s string) string {
	return strings.TrimSpace(os.ExpandEnv(strings.Trim(strings.TrimSpace(s), `"`)))
}

// splitArgs cuts a command line into arguments the way a Windows program
// would for the cases that matter here: spaces outside double quotes.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case (r == ' ' || r == '\t') && !quoted:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// toUTF8 accepts what schtasks may print: UTF-16 with its mark, UTF-8 with
// or without one, or else the console's code page.
func toUTF8(b []byte) []byte {
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		u := make([]uint16, (len(b)-2)/2)
		for i := range u {
			u[i] = uint16(b[2+2*i]) | uint16(b[3+2*i])<<8
		}
		return []byte(string(utf16.Decode(u)))
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return b[3:]
	case utf8.Valid(b):
		return b
	}
	return fromConsole(b)
}
