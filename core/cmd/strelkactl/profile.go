package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"strelka/internal/ipc"
	"strelka/proto"
)

// callLong is for the methods that answer only when a download is over.
func callLong(ctx context.Context, c *ipc.Client, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	err := c.Call(ctx, method, params, result)
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("служба не ответила на %s за 3 минуты", method)
	}
	return err
}

func getState(ctx context.Context, c *ipc.Client) (proto.State, error) {
	var st proto.State
	err := call(ctx, c, proto.MethodStateGet, nil, &st)
	return st, err
}

var modeText = map[string]string{
	proto.ModeRule:   "смарт: через VPN только списки",
	proto.ModeGlobal: "весь трафик через VPN",
}

func subsCmd(ctx context.Context, c *ipc.Client, args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		st, err := getState(ctx, c)
		if err != nil {
			return err
		}
		if len(st.Subs) == 0 {
			fmt.Println("Подписок нет. Добавить: strelkactl subs add ССЫЛКА [ИМЯ]")
			return nil
		}
		for _, s := range st.Subs {
			line := fmt.Sprintf("%-4s %-20s серверов: %-3d %s", s.ID, s.Name, s.Servers, s.URL)
			if s.UpdatedAt != nil {
				line += "  обновлена " + s.UpdatedAt.Local().Format("02.01 15:04")
			}
			fmt.Println(line)
			if s.ExpireAt != nil || s.TotalBytes != nil {
				extra := "    "
				if s.UsedBytes != nil && s.TotalBytes != nil {
					extra += fmt.Sprintf(" трафик %s из %s", bytesText(*s.UsedBytes), bytesText(*s.TotalBytes))
				}
				if s.ExpireAt != nil {
					extra += "  до " + s.ExpireAt.Local().Format("02.01.2006")
				}
				fmt.Println(extra)
			}
			if s.Error != nil {
				fmt.Printf("     не обновилась: %s\n", *s.Error)
			}
		}
		return nil
	case "add":
		if len(args) < 2 || len(args) > 3 {
			return errors.New("нужна ссылка и, если хочешь, короткое имя: strelkactl subs add ССЫЛКА [ИМЯ]")
		}
		p := proto.SubsAddParams{URL: args[1]}
		if len(args) == 3 {
			p.Name = args[2]
		}
		var res proto.SubsAddResult
		if err := callLong(ctx, c, proto.MethodSubsAdd, p, &res); err != nil {
			return err
		}
		fmt.Printf("Подписка %s добавлена, серверов: %d\n", res.ID, res.Servers)
		return nil
	case "link":
		if len(args) != 2 {
			return errors.New("нужна ссылка сервера: strelkactl subs link vless://…")
		}
		var res proto.IDParams
		if err := callLong(ctx, c, proto.MethodSubsAddLink, proto.SubsAddLinkParams{Link: args[1]}, &res); err != nil {
			return err
		}
		fmt.Printf("Сервер добавлен: %s\n", res.ID)
		return nil
	case "update":
		var p proto.SubsUpdateParams
		if len(args) > 1 {
			p.ID = args[1]
		}
		err := callLong(ctx, c, proto.MethodSubsUpdate, p, nil)
		// Print the list either way: it shows which one failed and why.
		subsCmd(ctx, c, []string{"list"})
		return err
	case "rm":
		if len(args) != 2 {
			return errors.New("нужен номер подписки: strelkactl subs rm s1")
		}
		return call(ctx, c, proto.MethodSubsRemove, proto.IDParams{ID: args[1]}, nil)
	case "rename":
		if len(args) < 3 {
			return errors.New("нужны номер и новое имя: strelkactl subs rename s1 Основная")
		}
		return call(ctx, c, proto.MethodSubsRename, proto.SubsRenameParams{ID: args[1], Name: strings.Join(args[2:], " ")}, nil)
	}
	return fmt.Errorf("неизвестная команда «subs %s»: есть list, add, link, update, rm, rename", args[0])
}

func bytesText(n int64) string {
	const gb = 1 << 30
	if n >= gb {
		return fmt.Sprintf("%.1f ГБ", float64(n)/gb)
	}
	return fmt.Sprintf("%.0f МБ", float64(n)/(1<<20))
}

func printServers(st proto.State) {
	if len(st.Servers) == 0 {
		fmt.Println("Серверов нет. Добавь подписку: strelkactl subs add ССЫЛКА")
		return
	}
	for _, s := range st.Servers {
		mark := " "
		if st.Server.Selected != nil && *st.Server.Selected == s.ID {
			mark = "*"
		}
		fav := " "
		if s.Favorite {
			fav = "+"
		}
		ping := "    —"
		switch {
		case s.Ping != nil:
			ping = fmt.Sprintf("%4d мс", *s.Ping)
		case s.PingAt != nil:
			ping = "молчит"
		}
		stats := ""
		if s.Stats != nil {
			stats = fmt.Sprintf("в среднем %d мс, промахов %.0f%%", s.Stats.Avg, s.Stats.Loss*100)
		}
		fmt.Printf("%s%s %-18s %-8s %-7s %-34s %s\n", mark, fav, s.ID, s.Via, ping, cut(s.Name, 34), stats)
	}
	fmt.Println("\n* — выбран, + — в избранном")
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func serversCmd(ctx context.Context, c *ipc.Client) error {
	st, err := getState(ctx, c)
	if err != nil {
		return err
	}
	printServers(st)
	return nil
}

func selectCmd(ctx context.Context, c *ipc.Client, args []string) error {
	if len(args) != 1 {
		return errors.New("нужен идентификатор сервера из «strelkactl servers»: strelkactl select lider-DE")
	}
	return call(ctx, c, proto.MethodServerSelect, proto.IDParams{ID: args[0]}, nil)
}

// testCmd starts a test round and waits until the daemon is done with it,
// saying how far it has got. Ctrl+C stops the round, not only the waiting.
func testCmd(ctx context.Context, c *ipc.Client, args []string) error {
	if err := call(ctx, c, proto.MethodServerTest, proto.ServerTestParams{IDs: args}, nil); err != nil {
		return err
	}
	said := ""
	for {
		st, err := getState(ctx, c)
		if err != nil {
			if ctx.Err() != nil {
				stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				call(stop, c, proto.MethodServerTestStop, nil, nil)
				cancel()
			}
			return err
		}
		if st.Test == nil {
			printServers(st)
			return nil
		}
		line := fmt.Sprintf("проверено %d из %d, отвечают %d", st.Test.Done, st.Test.Total, st.Test.Alive)
		if st.Test.Stage == proto.TestPreparing {
			line = "запускаю проверочный движок: VPN выключен"
		}
		if line != said {
			fmt.Println(line)
			said = line
		}
		select {
		case <-ctx.Done():
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func modeCmd(ctx context.Context, c *ipc.Client, args []string) error {
	if len(args) == 0 {
		st, err := getState(ctx, c)
		if err != nil {
			return err
		}
		fmt.Printf("%s (%s)\n", modeText[st.VPN.Mode], st.VPN.Mode)
		return nil
	}
	if len(args) != 1 {
		return errors.New("режим один: strelkactl mode rule или strelkactl mode global")
	}
	return call(ctx, c, proto.MethodModeSet, proto.ModeParams{Mode: args[0]}, nil)
}

var listText = map[string]string{proto.ListVPN: "Через VPN", proto.ListDirect: "Напрямую"}

func rulesCmd(ctx context.Context, c *ipc.Client, args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		var res proto.RulesGetResult
		if err := callLong(ctx, c, proto.MethodRulesGet, nil, &res); err != nil {
			return err
		}
		for _, name := range []string{proto.ListVPN, proto.ListDirect} {
			l := res.VPN
			if name == proto.ListDirect {
				l = res.Direct
			}
			fmt.Printf("%s (%s):\n", listText[name], name)
			printRules("  программы", proto.KindProcess, l.Processes)
			printRules("  сайты с поддоменами", proto.KindSuffix, l.Suffixes)
			printRules("  точные имена", proto.KindDomain, l.Domains)
			printRules("  слова в адресе", proto.KindKeyword, l.Keywords)
			printRules("  адреса", proto.KindIP, l.IPs)
		}
		for _, a := range res.Auto {
			fmt.Printf("Автосписок %s: записей %d\n", a.Tag, a.Count)
		}
		return nil
	case "add":
		// --kind may stand anywhere: "rules add vpn tele --kind keyword" is
		// how one would type it, and the flag package stops at "vpn".
		kind, rest := "", []string{}
		for i := 1; i < len(args); i++ {
			switch a := args[i]; {
			case a == "--kind" && i+1 < len(args):
				i++
				kind = args[i]
			case strings.HasPrefix(a, "--kind="):
				kind = strings.TrimPrefix(a, "--kind=")
			default:
				rest = append(rest, a)
			}
		}
		if len(rest) != 2 {
			return errors.New("нужны список и значение: strelkactl rules add vpn curl.exe")
		}
		var ref proto.RuleRef
		if err := call(ctx, c, proto.MethodRulesAdd, proto.RulesAddParams{List: rest[0], Value: rest[1], Kind: kind}, &ref); err != nil {
			return err
		}
		fmt.Printf("Добавлено в «%s»: %s (%s)\n", rest[0], ref.Value, ref.Kind)
		return nil
	case "rm":
		if len(args) != 4 {
			return errors.New("нужны список, вид и значение, как в «rules list»: strelkactl rules rm vpn process curl.exe")
		}
		return call(ctx, c, proto.MethodRulesRemove, proto.RuleRef{List: args[1], Kind: args[2], Value: args[3]}, nil)
	case "check":
		if len(args) != 2 {
			return errors.New("нужна программа, сайт или адрес: strelkactl rules check discord.com")
		}
		var res proto.RulesCheckResult
		if err := callLong(ctx, c, proto.MethodRulesCheck, proto.RulesCheckParams{Target: args[1]}, &res); err != nil {
			return err
		}
		where := "напрямую"
		if res.Route == proto.ListVPN {
			where = "через VPN"
		}
		fmt.Printf("%s: %s. %s\n", args[1], where, res.Because)
		return nil
	case "apps":
		var res proto.RulesProcessesResult
		if err := callLong(ctx, c, proto.MethodRulesProcesses, nil, &res); err != nil {
			return err
		}
		for _, p := range res.Processes {
			fmt.Printf("%-32s %s\n", p.Name, p.Path)
		}
		return nil
	}
	return fmt.Errorf("неизвестная команда «rules %s»: есть list, add, rm, check, apps", args[0])
}

func printRules(title, kind string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Printf("%s (%s): %s\n", title, kind, strings.Join(values, ", "))
}

func importLegacy(ctx context.Context, c *ipc.Client, args []string) error {
	if len(args) != 1 {
		return errors.New("нужна папка старой Стрелки: strelkactl import-legacy C:\\путь\\к\\папке")
	}
	// The daemon wants a full path: its working folder is not this one.
	dir, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	var res proto.LegacyImportResult
	if err := callLong(ctx, c, proto.MethodLegacyImport, proto.LegacyImportParams{Dir: dir}, &res); err != nil {
		return err
	}
	fmt.Printf("Перенесено: подписок %d, серверов %d, правил %d\n", res.Subs, res.Servers, res.Rules)
	sel := "не выбран"
	if res.Selected != nil {
		sel = *res.Selected
	}
	fmt.Printf("Сервер: %s. Режим: %s.\n", sel, modeText[res.Mode])
	if len(res.Failed) > 0 {
		fmt.Fprintf(os.Stderr, "Не скачались: %s. Причины — в «strelkactl subs list».\n", strings.Join(res.Failed, ", "))
	}
	return nil
}

// setCmd changes settings: strelkactl set localProxyPort=2080 ipv6=block.
// Values are JSON where that reads naturally (numbers, true/false) and plain
// text otherwise.
func setCmd(ctx context.Context, c *ipc.Client, args []string) error {
	if len(args) == 0 {
		st, err := getState(ctx, c)
		if err != nil {
			return err
		}
		out, _ := json.MarshalIndent(st.Settings, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	patch := map[string]json.RawMessage{}
	for _, a := range args {
		key, value, ok := strings.Cut(a, "=")
		if !ok || key == "" {
			return errors.New("настройки задаются как имя=значение: strelkactl set localProxyPort=2080")
		}
		raw := json.RawMessage(value)
		if !json.Valid(raw) {
			raw, _ = json.Marshal(value)
		}
		patch[key] = raw
	}
	return call(ctx, c, proto.MethodSettingsSet, map[string]any{"patch": patch}, nil)
}

// connsCmd prints one snapshot of the live connections: who is talking to
// whom and by which route. The daemon sends them once a second to whoever
// subscribes; one message is enough here.
func connsCmd(ctx context.Context, c *ipc.Client) error {
	if err := call(ctx, c, proto.MethodStateSubscribe, proto.TopicsParams{Topics: []string{proto.TopicConnections}}, nil); err != nil {
		return err
	}
	timeout := time.After(4 * time.Second)
	for {
		select {
		case n, ok := <-c.Notifications():
			if !ok {
				return ipc.ErrClosed
			}
			if n.Method != proto.TopicConnections {
				continue
			}
			var conns []proto.Connection
			if err := json.Unmarshal(n.Params, &conns); err != nil {
				return err
			}
			for _, cn := range conns {
				process := cn.Process
				if process == "" {
					process = "—"
				}
				fmt.Println(fmt.Sprintf("%-6s %-28s %-40s ", cn.Route, cut(process, 28), cut(cn.Host, 40)) + strings.Join(cn.Chain, " <- "))
			}
			fmt.Println("Соединений:", len(conns))
			return nil
		case <-timeout:
			return errors.New("служба не прислала список соединений: VPN включён?")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func versionsCmd(ctx context.Context, c *ipc.Client, hello proto.HelloResult) error {
	var res proto.EngineVersionsResult
	// The first answer runs both engines once; that is more than a moment.
	if err := callLong(ctx, c, proto.MethodEngineVersions, nil, &res); err != nil {
		return err
	}
	fmt.Printf("strelkad  %s (протокол %d, %s)\n", hello.Version, hello.Proto, hello.OS)
	for _, e := range res.Engines {
		v := "не найден"
		if e.Version != nil {
			v = *e.Version
		}
		fmt.Printf("%-9s %s\n", e.Name, v)
	}
	return nil
}

func reportCmd(ctx context.Context, c *ipc.Client) error {
	var res proto.DiagExportResult
	if err := callLong(ctx, c, proto.MethodDiagExport, nil, &res); err != nil {
		return err
	}
	fmt.Println("Отчёт собран:", res.Path)
	return nil
}
