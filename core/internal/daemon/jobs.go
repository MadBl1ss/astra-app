package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"strelka/internal/confgen"
	"strelka/internal/incident"
	"strelka/internal/procs"
	"strelka/internal/profile"
	"strelka/internal/rules"
	"strelka/internal/subs"
	"strelka/proto"
)

// subsJob is one piece of work that ends in a new server list: adding a
// subscription or a link, refreshing subscriptions, importing the old
// Strelka. It runs in its own goroutine on a copy of the profile; only the
// finished result goes back to the loop, so a download that takes half a
// minute never holds the loop up and a failed one leaves nothing behind.
type subsJob struct {
	add    *proto.SubsAddParams
	link   string
	ids    []string // subscriptions to refresh; nil with all
	all    bool
	legacy *legacyData
	// reply answers the caller; nil for the timer.
	reply func(any, *proto.Error)
}

// outcome is how one subscription's download went.
type outcome struct {
	id, name string
	servers  int
	skipped  []subs.Skipped
	err      error
}

// subsResult is what a job hands back to the loop.
type subsResult struct {
	cand     *profile.Profile // nil: nothing to adopt
	outcomes []outcome
	newSub   string // subs.add
	newLink  string // subs.addLink
	err      *proto.Error
}

func (j *subsJob) answer(result any, err *proto.Error) {
	if j.reply != nil {
		j.reply(result, err)
	}
}

// byUser tells a job somebody asked for from the timer's own.
func (j *subsJob) byUser() bool { return j.reply != nil }

func later(c rpcCall) func(any, *proto.Error) {
	return func(result any, err *proto.Error) { c.reply <- rpcReply{result: result, err: err} }
}

func (d *Daemon) subsAdd(c rpcCall) (any, *proto.Error, bool) {
	var p proto.SubsAddParams
	if err := params(c, &p); err != nil {
		return nil, err, false
	}
	if err := d.jobBusy(); err != nil {
		return nil, err, false
	}
	// Refuse what will not parse before anything is downloaded: an
	// encrypted Happ link is told apart here, not after half a minute.
	if _, _, err := profile.CleanURL(p.URL); err != nil {
		return nil, profileError(err, ""), false
	}
	d.startSubsJob(&subsJob{add: &p, reply: later(c)})
	return nil, nil, true
}

func (d *Daemon) subsAddLink(c rpcCall) (any, *proto.Error, bool) {
	var p proto.SubsAddLinkParams
	if err := params(c, &p); err != nil {
		return nil, err, false
	}
	if err := d.jobBusy(); err != nil {
		return nil, err, false
	}
	d.startSubsJob(&subsJob{link: p.Link, reply: later(c)})
	return nil, nil, true
}

func (d *Daemon) subsUpdate(c rpcCall) (any, *proto.Error, bool) {
	var p proto.SubsUpdateParams
	if len(c.params) > 0 {
		if err := params(c, &p); err != nil {
			return nil, err, false
		}
	}
	if err := d.jobBusy(); err != nil {
		return nil, err, false
	}
	job := &subsJob{all: true, reply: later(c)}
	if p.ID != "" {
		if d.prof.Sub(p.ID) == nil {
			return nil, proto.Errorf(proto.CodeNotFound, "Нет такой подписки."), false
		}
		job.all, job.ids = false, []string{p.ID}
	} else if len(d.prof.Subs) == 0 {
		return nil, proto.Errorf(proto.CodeNotFound, "Нет ни одной подписки. Сначала добавь её."), false
	}
	d.startSubsJob(job)
	return nil, nil, true
}

// autoUpdate is the timer's refresh of every subscription.
func (d *Daemon) autoUpdate() {
	every, _ := autoUpdateEvery(d.st.Settings.AutoUpdateSubs)
	if d.job != "" || !d.prof.Due(time.Now(), every) {
		return
	}
	d.startSubsJob(&subsJob{all: true})
}

func (d *Daemon) startSubsJob(j *subsJob) {
	// The device id is made and saved before the first request carries it:
	// a job that fails must not lead to a second id next time.
	if !profile.ValidHWID(d.prof.HWID) {
		d.prof.EnsureHWID()
		d.saveProfile()
	}
	if j.byUser() {
		d.acted()
	}
	d.setJob(proto.BusyUpdatingSubs)
	cand := d.prof.Clone()
	settings := d.st.Settings
	var prevPorts map[string]int
	if pl := d.plan.Load(); pl != nil {
		prevPorts = pl.ports
	}
	go func() {
		res := d.runSubsJob(j, cand, settings, prevPorts)
		d.post(func() { d.adoptSubs(j, res) })
	}()
}

// runSubsJob does the slow part. It owns cand and touches nothing else.
func (d *Daemon) runSubsJob(j *subsJob, cand *profile.Profile, settings proto.Settings, prevPorts map[string]int) subsResult {
	var res subsResult
	old := cand.Clone().Servers
	useXray := d.hasXray()
	ids := j.ids

	if j.legacy != nil {
		if profile.ValidHWID(j.legacy.hwid) {
			// The old Strelka's id is the one the provider has on record.
			cand.HWID = j.legacy.hwid
		}
		for _, e := range j.legacy.subs {
			if _, err := cand.AddLegacySub(e.URL, e.Name); err != nil && !errors.Is(err, profile.ErrExists) {
				res.outcomes = append(res.outcomes, outcome{name: e.Name, err: err})
			}
		}
	}
	if j.add != nil {
		sub, err := cand.AddSub(j.add.URL, j.add.Name)
		if err != nil {
			res.err = profileError(err, "")
			return res
		}
		res.newSub, ids = sub.ID, []string{sub.ID}
	}
	if j.link != "" {
		srv, err := cand.AddLink(j.link, useXray)
		if err != nil {
			res.err = profileError(err, "")
			return res
		}
		res.newLink = srv.ID
	}
	if j.all {
		ids = nil
		for _, s := range cand.Subs {
			ids = append(ids, s.ID)
		}
	}

	// Download side by side, apply in order: ids depend on who came first.
	fetched := make([]subs.Fetched, len(ids))
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			fetched[i], errs[i] = subs.Fetch(context.Background(), nil, url, cand.HWID)
		}(i, cand.Sub(id).URL)
	}
	wg.Wait()
	now := time.Now()
	for i, id := range ids {
		if moved := fetched[i].MovedTo; j.add != nil && errs[i] == nil && moved != "" {
			// A new subscription keeps the address its link led to, as a
			// wrapped link pasted by hand does. Old ones keep theirs: the
			// redirect is followed again next time.
			if err := cand.MoveSub(id, moved); err != nil {
				res.err = profileError(err, "")
				return res
			}
		}
		o := outcome{id: id, name: cand.Sub(id).Name, err: errs[i]}
		if o.err != nil {
			cand.FetchFailed(id, o.err, now)
		} else {
			o.servers, o.skipped, o.err = cand.ApplyFetch(id, fetched[i], useXray, now)
			o.name = cand.Sub(id).Name // the panel may have named it
		}
		res.outcomes = append(res.outcomes, o)
	}
	if j.add != nil && res.outcomes[len(res.outcomes)-1].err != nil {
		res.err = proto.Errorf(proto.CodeFetchFailed, "Подписка не добавлена: %s.", res.outcomes[len(res.outcomes)-1].err)
		return res
	}

	// Servers the engine will not take must not get into the profile: the
	// VPN would refuse to start with them.
	if len(cand.Servers) > 0 && !profile.SameServers(old, cand.Servers) {
		if err := d.vetProfile(cand, settings, prevPorts); err != nil {
			d.opts.Logf("new servers rejected: %v", err)
			if j.add != nil || j.link != "" {
				res.err = proto.Errorf(proto.CodeInvalid, "Не добавлено: движок не принял настройки этого сервера.")
				return res
			}
			rejected := errors.New("движок не принял новые серверы")
			cand.Servers = old
			for i := range res.outcomes {
				if res.outcomes[i].err == nil {
					res.outcomes[i].err = rejected
					cand.FetchFailed(res.outcomes[i].id, rejected, now)
				}
			}
		}
	}
	res.cand = cand
	return res
}

func (d *Daemon) vetProfile(cand *profile.Profile, settings proto.Settings, prevPorts map[string]int) error {
	pl, err := d.planFor(cand, settings, prevPorts)
	if err != nil {
		return err
	}
	err = d.vet(context.Background(), pl, "check")
	if errors.Is(err, fs.ErrNotExist) {
		// No engine to ask. The start will say so itself.
		return nil
	}
	return err
}

// adoptSubs is the job's last step, back in the loop.
func (d *Daemon) adoptSubs(j *subsJob, res subsResult) {
	d.setJob("")
	if res.err != nil {
		d.refresh()
		j.answer(nil, res.err)
		return
	}
	prev := d.prof.Servers
	// The job worked on a copy. Whatever the user changed meanwhile — the
	// selection, the favourites, the mode — stays as it is now.
	d.prof.Subs, d.prof.Servers = res.cand.Subs, res.cand.Servers
	d.prof.NextSub, d.prof.HWID = res.cand.NextSub, res.cand.HWID

	failed := 0
	var failedNames []string
	var firstErr error
	for _, o := range res.outcomes {
		if o.err != nil {
			failed++
			failedNames = append(failedNames, o.name)
			if firstErr == nil {
				firstErr = o.err
			}
			kept := ""
			if n := d.prof.CountOf(o.id); n > 0 {
				kept = fmt.Sprintf(" Прежние серверы остались: %d.", n)
			}
			d.log(proto.CatSubs, proto.LevelProblem, fmt.Sprintf("Подписка «%s» не обновилась: %s.%s", o.name, o.err, kept))
			continue
		}
		text := fmt.Sprintf("Подписка «%s» обновлена: серверов %d.", o.name, o.servers)
		for _, s := range o.skipped {
			text += fmt.Sprintf(" Пропущено %d — %s.", s.Count, s.Reason)
		}
		d.log(proto.CatSubs, proto.LevelInfo, text)
	}
	if res.newLink != "" {
		d.log(proto.CatServer, proto.LevelInfo, "Добавлен сервер "+d.serverName(res.newLink)+".")
	}

	var imported *proto.LegacyImportResult
	if j.legacy != nil {
		imported = d.adoptLegacy(j.legacy, failedNames)
	}

	if profile.SameServers(prev, d.prof.Servers) {
		d.prof.FixSelection()
		d.saveProfile()
		d.refresh()
		d.replan()
		d.touch()
	} else {
		if j.byUser() {
			d.acted() // the engines restart on the new list
		}
		d.afterServersChanged("изменился список серверов")
	}
	d.checkSubs()
	if !j.byUser() && failed < len(res.outcomes) {
		d.toast(d.im.Notice(d.st.Settings.Notify, incident.SubsRefreshed(len(d.prof.Servers)))...)
	}

	switch {
	case imported != nil:
		imported.Servers = len(d.prof.Servers)
		if d.prof.Selected != "" {
			sel := d.prof.Selected
			imported.Selected = &sel
		}
		j.answer(imported, nil)
	case res.newSub != "":
		j.answer(proto.SubsAddResult{ID: res.newSub, Servers: d.prof.CountOf(res.newSub)}, nil)
	case res.newLink != "":
		j.answer(proto.IDParams{ID: res.newLink}, nil)
	case len(res.outcomes) > 0 && failed == len(res.outcomes):
		j.answer(nil, proto.Errorf(proto.CodeFetchFailed, "Подписки не обновились: %s. Прежние серверы остались.", firstErr))
	default:
		j.answer(nil, nil)
	}
}

// ---- rules: the slow answers ---------------------------------------------

// autoListInfo caches the size of an auto list for the file it was counted
// from; counting means decompiling the list with the engine.
type autoListInfo struct {
	mod   time.Time
	size  int64
	count int
}

func (d *Daemon) rulesGet(c rpcCall) (any, *proto.Error, bool) {
	res := proto.RulesGetResult{VPN: d.rules.VPN, Direct: d.rules.Direct, Auto: []proto.AutoList{}}
	type pending struct {
		tag, path string
		info      autoListInfo
	}
	var todo []pending
	for _, tag := range confgen.AutoLists() {
		path := confgen.AutoListFile(d.opts.Paths.RulesDir(), tag)
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		mod := fi.ModTime().UTC().Truncate(time.Second)
		al := proto.AutoList{Tag: tag, UpdatedAt: &mod}
		if cached, ok := d.autoInfo[tag]; ok && cached.mod.Equal(mod) && cached.size == fi.Size() {
			al.Count = cached.count
		} else {
			todo = append(todo, pending{tag: tag, path: path, info: autoListInfo{mod: mod, size: fi.Size()}})
		}
		res.Auto = append(res.Auto, al)
	}
	if len(todo) == 0 {
		return res, nil, false
	}
	exe := d.opts.Paths.Engine(boxName)
	go func() {
		for i := range todo {
			n, err := confgen.CountAutoList(context.Background(), exe, todo[i].path)
			if err != nil {
				d.opts.Logf("counting %s: %v", todo[i].tag, err)
				n = -1
			}
			todo[i].info.count = n
		}
		d.post(func() {
			for _, t := range todo {
				if t.info.count < 0 {
					continue
				}
				d.autoInfo[t.tag] = t.info
				for i := range res.Auto {
					if res.Auto[i].Tag == t.tag {
						res.Auto[i].Count = t.info.count
					}
				}
			}
			c.reply <- rpcReply{result: res}
		})
	}()
	return nil, nil, true
}

func (d *Daemon) rulesProcesses(c rpcCall) (any, *proto.Error, bool) {
	go func() {
		list, err := procs.List()
		if err != nil {
			d.opts.Logf("listing processes: %v", err)
			c.reply <- rpcReply{err: proto.Errorf(proto.CodeUnsupported, "Не удалось получить список запущенных программ.")}
			return
		}
		c.reply <- rpcReply{result: proto.RulesProcessesResult{Processes: list}}
	}()
	return nil, nil, true
}

func (d *Daemon) rulesCheck(c rpcCall) (any, *proto.Error, bool) {
	var p proto.RulesCheckParams
	if err := params(c, &p); err != nil {
		return nil, err, false
	}
	kind, value, err := rules.Normalize("", p.Target)
	if err != nil {
		return nil, profileError(err, ""), false
	}
	// The lists are copied: the answer is worked out off the loop, because
	// the auto lists are asked of the engine's own matcher.
	set := rules.Set{VPN: cloneList(d.rules.VPN), Direct: cloneList(d.rules.Direct)}
	global := d.prof.Mode == proto.ModeGlobal
	exe, dir := d.opts.Paths.Engine(boxName), d.opts.Paths.RulesDir()
	go func() {
		c.reply <- rpcReply{result: checkTarget(set, global, kind, value, func(tag, target string) bool {
			hit, err := confgen.MatchAutoList(context.Background(), exe, confgen.AutoListFile(dir, tag), target)
			if err != nil {
				d.opts.Logf("matching %s: %v", tag, err)
			}
			return hit
		})}
	}()
	return nil, nil, true
}

func cloneList(l rules.List) rules.List {
	return rules.List{
		Processes: append([]string(nil), l.Processes...),
		Domains:   append([]string(nil), l.Domains...),
		Suffixes:  append([]string(nil), l.Suffixes...),
		Keywords:  append([]string(nil), l.Keywords...),
		IPs:       append([]string(nil), l.IPs...),
	}
}

var kindTitle = map[string]string{
	proto.KindProcess: "программа",
	proto.KindDomain:  "сайт",
	proto.KindSuffix:  "сайт",
	proto.KindKeyword: "слово",
	proto.KindIP:      "адрес",
}

// checkTarget walks the route rules in the order the engine applies them and
// says where a program, a site or an address will go. inAuto asks whether a
// site or an address is in one of the auto lists.
func checkTarget(set rules.Set, global bool, kind, value string, inAuto func(tag, target string) bool) proto.RulesCheckResult {
	direct := func(why string) proto.RulesCheckResult {
		return proto.RulesCheckResult{Route: proto.ListDirect, Because: why}
	}
	vpn := func(why string) proto.RulesCheckResult {
		return proto.RulesCheckResult{Route: proto.ListVPN, Because: why}
	}

	bare := value
	if kind == proto.KindIP {
		if pfx, err := netip.ParsePrefix(value); err == nil {
			if pfx.Addr().IsPrivate() || pfx.Addr().IsLoopback() || pfx.Addr().IsLinkLocalUnicast() {
				return direct("Это адрес локальной сети: такие всегда идут напрямую.")
			}
			if pfx.IsSingleIP() {
				bare = pfx.Addr().String()
			}
		}
	}
	if rk, rule, ok := rules.Match(set.Direct, kind, value); ok {
		return direct(fmt.Sprintf("В списке «напрямую»: %s %s.", kindTitle[rk], rule))
	}
	if global {
		return vpn("Включён режим «Весь трафик».")
	}
	if rk, rule, ok := rules.Match(set.VPN, kind, value); ok {
		return vpn(fmt.Sprintf("В списке «через VPN»: %s %s.", kindTitle[rk], rule))
	}
	if kind == proto.KindProcess {
		return direct("Программы нет в списках. Её соединения идут по спискам сайтов, остальное — напрямую.")
	}
	if !strings.Contains(bare, "/") {
		for _, tag := range confgen.AutoLists() {
			if inAuto(tag, bare) {
				return vpn(fmt.Sprintf("В автосписке заблокированных сайтов (%s).", tag))
			}
		}
	}
	return direct("Нет ни в одном списке.")
}
