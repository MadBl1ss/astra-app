package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"strelka/internal/diag"
	"strelka/internal/runx"
	"strelka/internal/store"
	"strelka/proto"
)

// ---- engine versions -----------------------------------------------------

// versionInfo is what an engine file said about its version. It is kept
// while the file stays the same, so "О программе" does not start two
// processes every time it is opened.
type versionInfo struct {
	mod     time.Time
	size    int64
	version string
}

// versionRe reads "sing-box version 1.14.0" and "Xray 26.3.27 (Xray, …)".
var versionRe = regexp.MustCompile(`(?i)(?:sing-box version|xray)\s+v?(\d+\.\d+(?:\.\d+)?(?:[-+][0-9a-z.\-]+)?)`)

// parseVersion finds the version in what "<engine> version" printed.
func parseVersion(out []byte) string {
	if m := versionRe.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

// askVersion runs the engine once to read its version.
func askVersion(exe string) string {
	out, err := runx.Output(context.Background(), 10*time.Second, exe, filepath.Dir(exe), "version")
	if err != nil {
		return ""
	}
	return parseVersion(out)
}

func (d *Daemon) engineVersions(c rpcCall) (any, *proto.Error, bool) {
	type todo struct {
		at   int
		name string
		exe  string
		info versionInfo
	}
	res := proto.EngineVersionsResult{}
	var ask []todo
	for i, name := range []string{boxName, xrayName} {
		res.Engines = append(res.Engines, proto.EngineVersion{Name: name})
		exe := d.opts.Paths.Engine(name)
		fi, err := os.Stat(exe)
		if err != nil {
			continue // no file: the version stays null
		}
		info := versionInfo{mod: fi.ModTime(), size: fi.Size()}
		if have, ok := d.versions[name]; ok && have.mod.Equal(info.mod) && have.size == info.size {
			if have.version != "" {
				v := have.version
				res.Engines[i].Version = &v
			}
			continue
		}
		ask = append(ask, todo{at: i, name: name, exe: exe, info: info})
	}
	if len(ask) == 0 {
		return res, nil, false
	}
	// Starting a process takes a while; the loop does not wait for it.
	go func() {
		for i := range ask {
			ask[i].info.version = askVersion(ask[i].exe)
			if v := ask[i].info.version; v != "" {
				res.Engines[ask[i].at].Version = &v
			}
		}
		d.post(func() {
			for _, a := range ask {
				d.versions[a.name] = a.info
			}
		})
		c.reply <- rpcReply{result: res}
	}()
	return nil, nil, true
}

// ---- the report for support ---------------------------------------------

const (
	reportPrefix = "strelka-report-"
	reportsKept  = 5
	// tailBytes is how much of each log goes into the report: hours of a
	// busy engine's output, and the archive stays small enough to mail.
	tailBytes = 2 << 20
)

// reportInput is what the loop hands to the goroutine that builds the
// archive: copies only, nothing shared with the loop's state.
type reportInput struct {
	paths   store.Paths
	state   json.RawMessage
	rules   proto.RulesGetResult
	engines proto.EngineStatusResult
	secrets []string
	noTUN   bool
}

func (d *Daemon) diagExport(c rpcCall) (any, *proto.Error, bool) {
	in := reportInput{
		paths:   d.opts.Paths,
		state:   d.snapshot(),
		rules:   proto.RulesGetResult{VPN: cloneList(d.rules.VPN), Direct: cloneList(d.rules.Direct), Auto: []proto.AutoList{}},
		engines: d.engineStatus(),
		secrets: d.secrets(),
		noTUN:   d.opts.NoTUN,
	}
	go func() {
		path, err := buildReport(in, time.Now())
		if err != nil {
			d.opts.Logf("building the report: %v", err)
			c.reply <- rpcReply{err: proto.Errorf(proto.CodeInvalid, "Отчёт не собрался: не удалось записать файл. Подробности — в логе службы.")}
			return
		}
		d.post(func() {
			d.log(proto.CatSystem, proto.LevelInfo, "Собран отчёт для поддержки: "+filepath.Base(path)+".")
		})
		c.reply <- rpcReply{result: proto.DiagExportResult{Path: path}}
	}()
	return nil, nil, true
}

// secrets lists what would let someone use the user's servers or
// subscriptions: keys and passwords of every server, the subscription links
// with their tokens, the device id the providers count devices by.
func (d *Daemon) secrets() []string {
	var out []string
	if d.prof.HWID != "" {
		out = append(out, d.prof.HWID)
	}
	for _, s := range d.prof.Subs {
		out = append(out, s.URL)
		u, err := url.Parse(s.URL)
		if err != nil {
			continue
		}
		// The token is a path segment or a query value; a log line may
		// quote it without the rest of the link.
		for _, seg := range strings.Split(u.Path, "/") {
			if len(seg) >= 16 {
				out = append(out, seg)
			}
		}
		for _, values := range u.Query() {
			out = append(out, values...)
		}
	}
	for _, s := range d.prof.Servers {
		// Only the outbounds: the server's own "id" is its tag, which the
		// logs need to stay readable.
		out = append(out, diag.Secrets(s.Outbound)...)
		out = append(out, diag.Secrets(s.Xray)...)
	}
	return out
}

func buildReport(in reportInput, now time.Time) (string, error) {
	red := diag.NewRedactor(in.secrets)
	var files []diag.File
	add := func(name string, data []byte) {
		if len(data) > 0 {
			files = append(files, diag.File{Name: name, Data: red.Text(data)})
		}
	}
	indent := func(v any) []byte {
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return nil
		}
		return append(raw, '\n')
	}

	versions := proto.EngineVersionsResult{}
	for _, name := range []string{boxName, xrayName} {
		ev := proto.EngineVersion{Name: name}
		if v := askVersion(in.paths.Engine(name)); v != "" {
			ev.Version = &v
		}
		versions.Engines = append(versions.Engines, ev)
	}

	add("README.txt", []byte(reportReadme))
	add("about.txt", aboutText(in, versions, now))

	var state bytes.Buffer
	if json.Indent(&state, in.state, "", "  ") == nil {
		add("state.json", append(state.Bytes(), '\n'))
	}
	add("rules.json", indent(in.rules))
	add("engines.json", indent(map[string]any{"status": in.engines.Engines, "versions": versions.Engines}))

	journal, err := diag.Tail(in.paths.Journal(), tailBytes)
	if err != nil {
		return "", err
	}
	add("journal.jsonl", journal)

	for _, name := range []string{boxName, xrayName} {
		for _, path := range []string{in.paths.EngineLog(name) + ".1", in.paths.EngineLog(name)} {
			data, err := diag.Tail(path, tailBytes)
			if err != nil {
				return "", err
			}
			add("logs/"+filepath.Base(path), data)
		}
	}

	// The engines' configs hold every key; only their masked copies go in,
	// and a config that cannot be read as JSON is left out entirely.
	for _, path := range []string{in.paths.RunConfig(), in.paths.XrayConfig()} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		masked, err := diag.RedactJSON(raw)
		if err != nil {
			continue
		}
		add("config/"+filepath.Base(path), append(masked, '\n'))
	}

	dir := in.paths.ReportsDir()
	path := filepath.Join(dir, reportPrefix+now.Format("20060102-150405")+".zip")
	if err := diag.Write(path, files, now); err != nil {
		return "", err
	}
	diag.Prune(dir, reportPrefix, reportsKept)
	return path, nil
}

// reportReadme is for whoever opens the archive, the user first of all.
const reportReadme = `Отчёт Стрелки для поддержки.

Внутри: состояние VPN, журнал событий, логи движков и их настройки.
Ключи и пароли серверов, ссылки подписок и идентификатор устройства
заменены на ***: по этому архиву твоими серверами воспользоваться нельзя.
Адреса серверов и списки правил остались — без них не разобраться.
`

func aboutText(in reportInput, versions proto.EngineVersionsResult, now time.Time) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "strelkad %s, protocol %d\n", Version, proto.Version)
	fmt.Fprintf(&b, "system: %s/%s, %s\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Fprintf(&b, "made: %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(&b, "no TUN (development mode): %v\n", in.noTUN)
	for _, ev := range versions.Engines {
		v := "unknown"
		if ev.Version != nil {
			v = *ev.Version
		}
		fmt.Fprintf(&b, "%s: %s (%s)\n", ev.Name, v, in.paths.Engine(ev.Name))
	}
	return []byte(b.String())
}
