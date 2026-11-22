package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"strelka/internal/clash"
	"strelka/internal/confgen"
	"strelka/internal/rules"
	"strelka/internal/stats"
	"strelka/internal/subs"
	"strelka/proto"
)

// legacyData is what was read from an old Strelka folder. The folder is
// only ever read: it may be the user's working VPN.
type legacyData struct {
	subs     []subs.ListEntry
	hwid     string
	rules    *rules.Set // nil when the folder has no rule files
	stats    *stats.Stats
	selected string // "" when it could not be learned
	mode     string // proto.ModeRule | proto.ModeGlobal
	live     bool   // selected and mode came from the running old Strelka
}

// maxLegacyFile bounds what the daemon will read from a folder a client
// named: the path comes from an unprivileged user and the daemon is not.
const maxLegacyFile = 4 << 20

func readLegacyFile(dir, name string) ([]byte, error) {
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	return io.ReadAll(io.LimitReader(f, maxLegacyFile))
}

// readLegacy collects what can be carried over. Values are taken apart and
// checked one by one; nothing from the folder is used as a file or a config.
func readLegacy(dir string) (*legacyData, *proto.Error) {
	notStrelka := proto.Errorf(proto.CodeInvalid, "В этой папке нет старой Стрелки: не нашлись ни subscription.txt, ни config.json.")
	if !filepath.IsAbs(dir) {
		return nil, proto.Errorf(proto.CodeInvalid, "Нужен полный путь к папке старой Стрелки.")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, notStrelka
	}
	ld := &legacyData{mode: proto.ModeRule}
	subTxt, errSub := readLegacyFile(dir, "subscription.txt")
	cfg, errCfg := readLegacyFile(dir, "config.json")
	if errSub != nil && errCfg != nil {
		return nil, notStrelka
	}
	ld.subs = subs.ParseList(string(subTxt))
	if raw, err := readLegacyFile(dir, "hwid.txt"); err == nil {
		ld.hwid = strings.TrimSpace(string(raw))
	}

	// Rules: both files are read with the same parser as our own, then every
	// value goes through Normalize, like one typed by the user would.
	rulesDir := filepath.Join(dir, "rules")
	_, errV := os.Stat(rules.File(rulesDir, rules.TagVPN))
	_, errD := os.Stat(rules.File(rulesDir, rules.TagDirect))
	if errV == nil || errD == nil {
		if set, err := rules.Load(rulesDir); err == nil {
			clean := rules.Sanitize(set)
			ld.rules = &clean
		}
	}
	// The old tray's stats file has the same shape as ours.
	if _, err := os.Stat(filepath.Join(dir, "server-stats.json")); err == nil {
		ld.stats = stats.Load(filepath.Join(dir, "server-stats.json"))
	}

	if errCfg == nil {
		ld.readSelection(cfg)
	}
	return ld, nil
}

// readSelection learns the selected server and the mode. The truth is in the
// old engine's cache.db, which only that engine can read, so it is asked
// over its Clash API when it runs; the config's default is the fallback.
func (ld *legacyData) readSelection(cfg []byte) {
	var c struct {
		Outbounds []struct {
			Type    string `json:"type"`
			Tag     string `json:"tag"`
			Default string `json:"default"`
		} `json:"outbounds"`
		Experimental struct {
			ClashAPI struct {
				Controller string `json:"external_controller"`
				Secret     string `json:"secret"`
			} `json:"clash_api"`
		} `json:"experimental"`
	}
	if json.Unmarshal(cfg, &c) != nil {
		return
	}
	for _, ob := range c.Outbounds {
		if ob.Type == "selector" && ob.Tag == confgen.SelectorTag {
			ld.selected = ob.Default
		}
	}
	addr := c.Experimental.ClashAPI.Controller
	// Loopback only: this is a GET to an address a client supplied.
	if !strings.HasPrefix(addr, "127.0.0.1:") && !strings.HasPrefix(addr, "localhost:") {
		return
	}
	api := clash.New(addr, c.Experimental.ClashAPI.Secret)
	defer api.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	px, err := api.Proxies(ctx)
	if err != nil {
		return
	}
	// Through "auto" down to the server actually in use.
	if leaf := clash.LeafOf(px, confgen.SelectorTag); leaf != "" && leaf != confgen.SelectorTag {
		ld.selected, ld.live = leaf, true
	}
	if mode, _, err := api.Mode(ctx); err == nil && strings.EqualFold(mode, confgen.ClashGlobal) {
		ld.mode = proto.ModeGlobal
	}
}

func (d *Daemon) legacyImport(c rpcCall) (any, *proto.Error, bool) {
	var p proto.LegacyImportParams
	if err := params(c, &p); err != nil {
		return nil, err, false
	}
	if err := d.jobBusy(); err != nil {
		return nil, err, false
	}
	// Reading the folder and asking the old engine are quick but not
	// instant; they go to a goroutine with the rest of the job.
	d.setJob(proto.BusyUpdatingSubs)
	dir := filepath.Clean(p.Dir)
	go func() {
		ld, perr := readLegacy(dir)
		d.post(func() {
			d.setJob("")
			if perr != nil {
				c.reply <- rpcReply{err: perr}
				return
			}
			d.log(proto.CatSystem, proto.LevelInfo, "Переношу настройки из старой Стрелки.")
			d.startSubsJob(&subsJob{legacy: ld, all: true, reply: later(c)})
		})
	}()
	return nil, nil, true
}

// adoptLegacy applies what does not go through the subscription job: the
// rules, the stats, the selection and the mode. It runs in the loop, after
// the profile has taken the job's servers.
func (d *Daemon) adoptLegacy(ld *legacyData, failed []string) *proto.LegacyImportResult {
	res := &proto.LegacyImportResult{Subs: len(ld.subs), Mode: ld.mode, Failed: failed}
	if res.Failed == nil {
		res.Failed = []string{}
	}
	if ld.rules != nil {
		// The old Strelka's lists predate everything the built-in ones have
		// gained since, so they get all of it, as an old install of ours
		// would on its update.
		d.rules = rules.WithAdditions(*ld.rules, 0)
		if err := d.saveRules(); err == nil {
			res.Rules = ld.rules.Count()
			d.log(proto.CatRules, proto.LevelInfo, fmt.Sprintf("Правила перенесены из старой Стрелки: %d.", res.Rules))
		}
	}
	if ld.stats != nil {
		for id, s := range ld.stats.Servers {
			if _, have := d.stats.Servers[id]; !have && s != nil {
				d.stats.Servers[id] = s
			}
		}
		if err := d.stats.Save(d.opts.Paths.StatsFile(), time.Now()); err != nil {
			d.opts.Logf("saving the stats: %v", err)
		}
	}

	// The old "auto" has no counterpart: the server it was on is taken when
	// the old Strelka could be asked, otherwise the best one by its stats.
	switch {
	case d.prof.Server(ld.selected) != nil:
		d.prof.Selected = ld.selected
	default:
		var pool []string
		for _, s := range d.prof.Servers {
			if s.Country != "RU" { // a Russian exit unblocks nothing
				pool = append(pool, s.ID)
			}
		}
		if order := d.stats.Order(pool, time.Now()); len(order) > 0 {
			d.prof.Selected = order[0]
		}
	}
	if d.prof.Mode != ld.mode {
		d.prof.Mode = ld.mode
		api, mode := d.api.Load(), confgen.ClashMode(ld.mode)
		d.engineDo("смена режима", func(ctx context.Context) error {
			if err := api.SetMode(ctx, mode); err != nil {
				return err
			}
			return api.CloseAll(ctx)
		})
	}
	if sel := d.prof.Selected; sel != "" {
		api := d.api.Load()
		d.engineDo("выбор сервера", func(ctx context.Context) error {
			return api.SwitchTo(ctx, confgen.SelectorTag, sel)
		})
	}
	d.log(proto.CatSystem, proto.LevelInfo, fmt.Sprintf("Из старой Стрелки перенесено подписок: %d.", len(ld.subs)))
	return res
}
