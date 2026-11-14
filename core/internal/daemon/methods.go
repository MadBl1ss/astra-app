package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"strelka/internal/confgen"
	"strelka/internal/profile"
	"strelka/internal/rules"
	"strelka/internal/store"
	"strelka/proto"
)

// dispatchProfile answers the methods about servers, subscriptions, rules
// and settings.
func (d *Daemon) dispatchProfile(c rpcCall) (any, *proto.Error, bool) {
	now := func(result any, err *proto.Error) (any, *proto.Error, bool) { return result, err, false }
	switch c.method {
	case proto.MethodModeSet:
		return now(d.modeSet(c))
	case proto.MethodServerSelect:
		return now(d.serverSelect(c))
	case proto.MethodServerFavorite:
		return now(d.serverFavorite(c))
	case proto.MethodServerReorder:
		return now(d.serverReorder(c))
	case proto.MethodServerRemove:
		return now(d.serverRemove(c))
	case proto.MethodServerTest:
		return now(d.serverTest(c))
	case proto.MethodSubsRename:
		return now(d.subsRename(c))
	case proto.MethodSubsRemove:
		return now(d.subsRemove(c))
	case proto.MethodSubsAdd:
		return d.subsAdd(c)
	case proto.MethodSubsAddLink:
		return d.subsAddLink(c)
	case proto.MethodSubsUpdate:
		return d.subsUpdate(c)
	case proto.MethodLegacyImport:
		return d.legacyImport(c)
	case proto.MethodRulesGet:
		return d.rulesGet(c)
	case proto.MethodRulesAdd:
		return now(d.rulesAdd(c))
	case proto.MethodRulesRemove:
		return now(d.rulesRemove(c))
	case proto.MethodRulesCheck:
		return d.rulesCheck(c)
	case proto.MethodRulesProcesses:
		return d.rulesProcesses(c)
	case proto.MethodSettingsSet:
		return now(d.settingsSet(c))
	}
	return nil, proto.Errorf(proto.CodeMethodNotFound, "Служба не знает метод «%s».", c.method), false
}

var errParams = proto.Errorf(proto.CodeInvalidParams, "Неверные параметры запроса.")

// params decodes the parameters of a call into v.
func params(c rpcCall, v any) *proto.Error {
	if len(c.params) == 0 {
		return errParams
	}
	if err := json.Unmarshal(c.params, v); err != nil {
		return errParams
	}
	return nil
}

// profileError turns the profile's and the rules' errors into protocol ones.
func profileError(err error, missing string) *proto.Error {
	var pinv *profile.Invalid
	var rinv *rules.Invalid
	switch {
	case errors.As(err, &pinv):
		return proto.Errorf(proto.CodeInvalid, "%s", pinv.Text)
	case errors.As(err, &rinv):
		return proto.Errorf(proto.CodeInvalid, "%s", rinv.Text)
	case errors.Is(err, profile.ErrNotFound):
		return proto.Errorf(proto.CodeNotFound, "%s", missing)
	case errors.Is(err, profile.ErrExists):
		return proto.Errorf(proto.CodeInvalid, "Такая подписка уже добавлена.")
	}
	return proto.Errorf(proto.CodeInvalid, "Не получилось: %v.", err)
}

// jobBusy refuses a change to the server list while a job that will replace
// it is running.
func (d *Daemon) jobBusy() *proto.Error {
	switch d.job {
	case proto.BusyUpdatingSubs:
		return proto.Errorf(proto.CodeBusy, "Подписки сейчас обновляются. Подожди, пока закончится.")
	case proto.BusyTesting:
		return proto.Errorf(proto.CodeBusy, "Идёт проверка серверов. Подожди, пока закончится.")
	}
	return nil
}

func (d *Daemon) saveProfile() {
	if err := d.prof.Save(d.opts.Paths.ProfileFile()); err != nil {
		d.opts.Logf("saving the profile: %v", err)
	}
}

// refresh rebuilds the parts of State that mirror the profile.
func (d *Daemon) refresh() {
	servers := make([]proto.Server, 0, len(d.prof.Servers))
	for _, s := range d.prof.Servers {
		ps := proto.Server{
			ID: s.ID, Name: s.Name, Sub: s.Sub, Protocol: s.Protocol, Via: s.Via, Host: s.Host,
			Order: d.prof.FavoriteOrder(s.ID),
		}
		ps.Favorite = ps.Order >= 0
		if !ps.Favorite {
			ps.Order = 0
		}
		if s.Country != "" {
			country := s.Country
			ps.Country = &country
		}
		if p, ok := d.pings[s.ID]; ok {
			at := p.at.UTC().Truncate(time.Second)
			ps.PingAt, ps.Alive = &at, p.alive
			if p.alive {
				ms := p.ms
				ps.Ping = &ms
			}
		}
		if st := d.stats.Servers[s.ID]; st != nil && (st.N > 0 || st.Miss > 0) {
			ps.Stats = &proto.ServerStats{Avg: int(st.Avg + 0.5), Loss: float64(int(st.Miss*100+0.5)) / 100, Samples: st.N}
		}
		servers = append(servers, ps)
	}
	d.st.Servers = servers

	subsOut := make([]proto.Sub, 0, len(d.prof.Subs))
	for _, s := range d.prof.Subs {
		ps := proto.Sub{
			ID: s.ID, Name: s.Name, URL: profile.MaskURL(s.URL), Servers: d.prof.CountOf(s.ID),
			UpdatedAt: s.UpdatedAt, ExpireAt: s.ExpireAt, UsedBytes: s.UsedBytes, TotalBytes: s.TotalBytes,
		}
		if s.Error != "" {
			text := s.Error
			ps.Error = &text
		}
		subsOut = append(subsOut, ps)
	}
	d.st.Subs = subsOut

	d.st.VPN.Mode = d.prof.Mode
	d.st.Server = proto.Selected{}
	if d.prof.Selected != "" {
		sel := d.prof.Selected
		d.st.Server.Selected = &sel
		if p, ok := d.pings[sel]; ok && p.alive {
			ms, at := p.ms, p.at.UTC().Truncate(time.Second)
			d.st.Server.Ping, d.st.Server.PingAt = &ms, &at
		}
	}
}

// serverName is how a server is called in the journal.
func (d *Daemon) serverName(id string) string {
	if s := d.prof.Server(id); s != nil && s.Name != "" && s.Name != id {
		return fmt.Sprintf("%s (%s)", s.Name, id)
	}
	return id
}

// engineDo runs a Clash API call against the running engine in the
// background and reports a failure in the journal. It is a no-op while the
// VPN is off: the next start takes everything from the plan.
func (d *Daemon) engineDo(what string, fn func(ctx context.Context) error) {
	api := d.api.Load()
	if api == nil || !d.live.Load() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := fn(ctx); err != nil {
			d.opts.Logf("%s: %v", what, err)
			d.post(func() {
				d.log(proto.CatVPN, proto.LevelProblem, "Движок не применил изменение ("+what+"). Помогает перезапуск VPN.")
			})
		}
	}()
}

func (d *Daemon) modeSet(c rpcCall) (any, *proto.Error) {
	var p proto.ModeParams
	if err := params(c, &p); err != nil {
		return nil, err
	}
	if p.Mode != proto.ModeRule && p.Mode != proto.ModeGlobal {
		return nil, proto.Errorf(proto.CodeInvalidParams, "Режим бывает rule или global.")
	}
	if d.prof.Mode == p.Mode {
		return nil, nil
	}
	d.prof.Mode = p.Mode
	d.saveProfile()
	d.replan()
	d.refresh()
	text := "Режим: через VPN идёт только список."
	if p.Mode == proto.ModeGlobal {
		text = "Режим: весь трафик через VPN."
	}
	d.log(proto.CatVPN, proto.LevelInfo, text)
	api, mode := d.api.Load(), confgen.ClashMode(p.Mode)
	d.engineDo("смена режима", func(ctx context.Context) error {
		if err := api.SetMode(ctx, mode); err != nil {
			return err
		}
		// Open connections were routed under the old mode and would keep
		// that route until they close on their own.
		return api.CloseAll(ctx)
	})
	d.touch()
	return nil, nil
}

func (d *Daemon) serverSelect(c rpcCall) (any, *proto.Error) {
	var p proto.IDParams
	if err := params(c, &p); err != nil {
		return nil, err
	}
	if err := d.prof.Select(p.ID); err != nil {
		return nil, profileError(err, "Нет такого сервера.")
	}
	d.saveProfile()
	d.replan()
	d.refresh()
	d.log(proto.CatServer, proto.LevelInfo, "Выбран сервер "+d.serverName(p.ID)+".")
	api := d.api.Load()
	// The selector interrupts the connections of the old server itself.
	d.engineDo("выбор сервера", func(ctx context.Context) error {
		return api.SwitchTo(ctx, confgen.SelectorTag, p.ID)
	})
	d.touch()
	return nil, nil
}

func (d *Daemon) serverFavorite(c rpcCall) (any, *proto.Error) {
	var p proto.ServerFavoriteParams
	if err := params(c, &p); err != nil {
		return nil, err
	}
	if err := d.prof.SetFavorite(p.ID, p.Favorite); err != nil {
		return nil, profileError(err, "Нет такого сервера.")
	}
	d.saveProfile()
	d.refresh()
	d.touch()
	return nil, nil
}

func (d *Daemon) serverReorder(c rpcCall) (any, *proto.Error) {
	var p proto.ServerReorderParams
	if err := params(c, &p); err != nil {
		return nil, err
	}
	if err := d.prof.Reorder(p.IDs); err != nil {
		return nil, profileError(err, "В списке есть сервер, которого нет в избранном.")
	}
	d.saveProfile()
	d.refresh()
	d.touch()
	return nil, nil
}

func (d *Daemon) serverRemove(c rpcCall) (any, *proto.Error) {
	var p proto.IDParams
	if err := params(c, &p); err != nil {
		return nil, err
	}
	if err := d.jobBusy(); err != nil {
		return nil, err
	}
	name := d.serverName(p.ID)
	if err := d.prof.RemoveServer(p.ID); err != nil {
		return nil, profileError(err, "Нет такого сервера.")
	}
	d.log(proto.CatServer, proto.LevelInfo, "Сервер "+name+" удалён.")
	d.afterServersChanged("изменился список серверов")
	return nil, nil
}

func (d *Daemon) subsRename(c rpcCall) (any, *proto.Error) {
	var p proto.SubsRenameParams
	if err := params(c, &p); err != nil {
		return nil, err
	}
	if err := d.jobBusy(); err != nil {
		return nil, err
	}
	if err := d.prof.RenameSub(p.ID, p.Name); err != nil {
		return nil, profileError(err, "Нет такой подписки.")
	}
	d.saveProfile()
	d.refresh()
	d.touch()
	return nil, nil
}

func (d *Daemon) subsRemove(c rpcCall) (any, *proto.Error) {
	var p proto.IDParams
	if err := params(c, &p); err != nil {
		return nil, err
	}
	if err := d.jobBusy(); err != nil {
		return nil, err
	}
	sub := d.prof.Sub(p.ID)
	if sub == nil {
		return nil, proto.Errorf(proto.CodeNotFound, "Нет такой подписки.")
	}
	name := sub.Name
	d.prof.RemoveSub(p.ID)
	d.log(proto.CatSubs, proto.LevelInfo, "Подписка «"+name+"» удалена вместе с серверами.")
	d.afterServersChanged("изменился список серверов")
	return nil, nil
}

// afterServersChanged is the common tail of everything that adds or removes
// servers: fix the selection, save, show, get the engines onto the new set.
func (d *Daemon) afterServersChanged(why string) {
	if lost := d.prof.FixSelection(); lost != "" && d.prof.Selected != "" {
		d.log(proto.CatServer, proto.LevelProblem,
			"Сервера "+lost+" больше нет — выбран "+d.serverName(d.prof.Selected)+".")
	}
	d.saveProfile()
	d.refresh()
	d.applyPlanChange(why)
	d.touch()
}

// ---- rules ---------------------------------------------------------------

var listTitle = map[string]string{rules.TagVPN: "через VPN", rules.TagDirect: "напрямую"}

func (d *Daemon) rulesAdd(c rpcCall) (any, *proto.Error) {
	var p proto.RulesAddParams
	if err := params(c, &p); err != nil {
		return nil, err
	}
	if !rules.ValidList(p.List) {
		return nil, proto.Errorf(proto.CodeInvalidParams, "Список бывает vpn или direct.")
	}
	kind, value, err := rules.Normalize(p.Kind, p.Value)
	if err != nil {
		return nil, profileError(err, "")
	}
	d.rules.Add(p.List, kind, value)
	if perr := d.saveRules(); perr != nil {
		return nil, perr
	}
	d.log(proto.CatRules, proto.LevelInfo, fmt.Sprintf("В список «%s» добавлено: %s.", listTitle[p.List], value))
	return proto.RuleRef{Kind: kind, Value: value}, nil
}

func (d *Daemon) rulesRemove(c rpcCall) (any, *proto.Error) {
	var p proto.RuleRef
	if err := params(c, &p); err != nil {
		return nil, err
	}
	if !rules.ValidList(p.List) {
		return nil, proto.Errorf(proto.CodeInvalidParams, "Список бывает vpn или direct.")
	}
	if !d.rules.Remove(p.List, p.Kind, p.Value) {
		return nil, proto.Errorf(proto.CodeNotFound, "Такого правила в списке нет.")
	}
	if perr := d.saveRules(); perr != nil {
		return nil, perr
	}
	d.log(proto.CatRules, proto.LevelInfo, fmt.Sprintf("Из списка «%s» убрано: %s.", listTitle[p.List], p.Value))
	return nil, nil
}

// saveRules rewrites the rule-set files that changed. The engine notices
// and reloads them; nothing is restarted.
func (d *Daemon) saveRules() *proto.Error {
	if err := rules.Save(d.opts.Paths.RulesDir(), d.rules); err != nil {
		d.opts.Logf("saving the rules: %v", err)
		return proto.Errorf(proto.CodeInvalid, "Не удалось записать правила на диск.")
	}
	return nil
}

// ---- settings ------------------------------------------------------------

// cleanSettings replaces values that make no sense (a file edited by hand,
// a newer version's value) with the defaults.
func cleanSettings(s proto.Settings) proto.Settings {
	def := proto.DefaultSettings()
	if s.Notify != "all" && s.Notify != "important" && s.Notify != "silent" {
		s.Notify = def.Notify
	}
	if s.IPv6 != "tunnel" && s.IPv6 != "block" {
		s.IPv6 = def.IPv6
	}
	if _, ok := autoUpdateEvery(s.AutoUpdateSubs); !ok {
		s.AutoUpdateSubs = def.AutoUpdateSubs
	}
	if !validProxyPort(s.LocalProxyPort) {
		s.LocalProxyPort = 0
	}
	return s
}

func validProxyPort(p int) bool { return p == 0 || (p >= 1024 && p <= 65535) }

// autoUpdateEvery reads the autoUpdateSubs setting; zero means "off".
func autoUpdateEvery(v string) (time.Duration, bool) {
	switch v {
	case "off":
		return 0, true
	case "6h":
		return 6 * time.Hour, true
	case "12h":
		return 12 * time.Hour, true
	case "24h":
		return 24 * time.Hour, true
	}
	return 0, false
}

func (d *Daemon) settingsSet(c rpcCall) (any, *proto.Error) {
	var p proto.SettingsSetParams
	if err := params(c, &p); err != nil {
		return nil, err
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(p.Patch, &patch); err != nil || patch == nil {
		return nil, proto.Errorf(proto.CodeInvalidParams, "Неверные параметры: patch должен быть объектом.")
	}
	next := d.st.Settings
	bad := func(key string) *proto.Error {
		return proto.Errorf(proto.CodeInvalidParams, "Настройка «%s»: такое значение не подходит.", key)
	}
	for key, raw := range patch {
		var err error
		switch key {
		case "notify":
			err = json.Unmarshal(raw, &next.Notify)
		case "quietFullscreen":
			err = json.Unmarshal(raw, &next.QuietFullscreen)
		case "guard":
			err = json.Unmarshal(raw, &next.Guard)
		case "allowLan":
			err = json.Unmarshal(raw, &next.AllowLAN)
		case "ipv6":
			err = json.Unmarshal(raw, &next.IPv6)
		case "failover":
			err = json.Unmarshal(raw, &next.Failover)
		case "autoUpdateSubs":
			err = json.Unmarshal(raw, &next.AutoUpdateSubs)
		case "localProxyPort":
			err = json.Unmarshal(raw, &next.LocalProxyPort)
		case "startOnBoot":
			err = json.Unmarshal(raw, &next.StartOnBoot)
		default:
			return nil, proto.Errorf(proto.CodeInvalidParams, "Нет такой настройки: «%s».", key)
		}
		if err != nil {
			return nil, bad(key)
		}
	}
	// A value cleanSettings would have to replace is a value to refuse.
	if cleaned := cleanSettings(next); cleaned != next {
		for _, key := range []string{"notify", "ipv6", "autoUpdateSubs", "localProxyPort"} {
			if _, sent := patch[key]; sent {
				return nil, bad(key)
			}
		}
		next = cleaned
	}
	prev := d.st.Settings
	if next == prev {
		return nil, nil
	}
	d.st.Settings = next
	d.st.Guard.Enabled, d.st.Guard.AllowLAN = next.Guard, next.AllowLAN
	if err := store.SaveJSON(d.opts.Paths.SettingsFile(), next); err != nil {
		d.opts.Logf("saving the settings: %v", err)
	}
	// These two are baked into the engine's config.
	if next.IPv6 != prev.IPv6 || next.LocalProxyPort != prev.LocalProxyPort {
		d.applyPlanChange("изменились настройки сети")
	}
	d.touch()
	return nil, nil
}
