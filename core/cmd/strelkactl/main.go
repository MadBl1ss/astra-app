// strelkactl talks to strelkad over its channel: for debugging, scripts and
// the checks of each stage.
//
//	strelkactl [--pipe NAME] status | start | stop | restart | watch | call <method> [json]
//	strelkactl subs | servers | select | test | mode | rules | set | import-legacy
//	strelkactl journal | action
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"strelka/internal/ipc"
	"strelka/proto"
)

const version = "0.1.0"

func main() { os.Exit(realMain(os.Args[1:])) }

func usage() {
	fmt.Fprint(os.Stderr, `Консольный клиент службы Стрелки.

  strelkactl [--pipe ИМЯ] команда

  status                 состояние VPN и движка
  start | stop           включить или выключить VPN и дождаться результата
  restart                перезапустить движок
  watch [--log] [--json] поток состояния, диагнозов, журнала и уведомлений;
                         --log добавляет лог движка
  journal [-n ЧИСЛО] [--problems] [--incident НОМЕР]
                         журнал, свежие записи внизу; --problems — только проблемы
  action ДЕЙСТВИЕ [НОМЕР]
                         нажать кнопку уведомления: switch:СЕРВЕР, retry, update_subs,
                         release (разрешить интернет без VPN)
  guard                  замок: включён ли, сколько фильтров стоит, закрыт ли интернет
  call МЕТОД [JSON]      вызвать любой метод службы и напечатать ответ

  subs [list]            подписки
  subs add ССЫЛКА [ИМЯ]  добавить подписку и скачать её серверы
  subs link ССЫЛКА       добавить один сервер по ссылке vless://, trojan://, ss://, hysteria2://
  subs update [НОМЕР]    обновить одну подписку или все
  subs rm НОМЕР          удалить подписку вместе с серверами
  subs rename НОМЕР ИМЯ  переименовать
  servers                серверы: кто выбран, пинг, статистика
  conns                  открытые соединения: программа, сайт, через VPN или напрямую
  select СЕРВЕР          выбрать сервер, без перезапуска VPN
  test [СЕРВЕР…]         проверить серверы (все или перечисленные); нужен включённый VPN
  mode [rule|global]     режим: смарт (через VPN только списки) или весь трафик
  rules [list]           списки «через VPN» и «напрямую»
  rules add СПИСОК ЗНАЧЕНИЕ [--kind ВИД]
                         СПИСОК — vpn или direct; ЗНАЧЕНИЕ — программа, сайт или адрес.
                         ВИД нужен для keyword (слово в адресе) и domain (точное имя)
  rules rm СПИСОК ВИД ЗНАЧЕНИЕ
  rules check ЦЕЛЬ       куда пойдёт программа, сайт или адрес и почему
  rules apps             запущенные программы, которые можно добавить в правила
  set [ИМЯ=ЗНАЧЕНИЕ…]    настройки службы; без параметров — показать
  import-legacy ПАПКА    перенести подписки, правила и выбор из старой Стрелки
  find-legacy            найти старые Стрелки: задача планировщика, запущенный трей
  retire-legacy          выключить старые Стрелки: остановить их программы и задачу
                         планировщика (файлы остаются); автозапуск значка в реестре
                         пользователя снимает окно
  check                  проверить соединение по шагам: сеть, интернет, DNS, VPN, сервер, сайт
  versions              версии службы и движков
  report                 собрать отчёт для поддержки (архив логов без ключей)
  place                  где компьютер — для глобуса в окне
  place detect           определить по IP сейчас и вернуться к автоматическому
  place set ШИРОТА ДОЛГОТА ГОРОД [СТРАНА]
                         выбрать вручную; СТРАНА — код из двух букв

  --pipe     имя канала; по умолчанию $STRELKA_PIPE, иначе strelka у установленной
             копии и strelka-dev у собранной для разработки
  --no-wait  для start, stop, restart: не ждать, пока фаза сменится
`)
}

// installedPipe is the channel of the installed service. The installer's
// build stamps "strelka" into the copy it ships (-ldflags -X); a developer's
// build stays on strelka-dev and never reaches the installed service.
var installedPipe string

func realMain(args []string) int {
	fs := flag.NewFlagSet("strelkactl", flag.ContinueOnError)
	fs.Usage = usage
	defPipe := os.Getenv("STRELKA_PIPE")
	if defPipe == "" {
		defPipe = installedPipe
	}
	if defPipe == "" {
		defPipe = ipc.DevName
	}
	pipe := fs.String("pipe", defPipe, "")
	noWait := fs.Bool("no-wait", false, "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		usage()
		return 2
	}
	cmd, rest := rest[0], rest[1:]
	switch cmd {
	case "status", "start", "stop", "restart", "watch", "call",
		"subs", "servers", "conns", "select", "test", "mode", "rules", "set", "import-legacy",
		"journal", "action", "versions", "report", "guard", "place", "check", "find-legacy", "retire-legacy":
	case "help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "Неизвестная команда «%s».\n\n", cmd)
		usage()
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	dialCtx, done := context.WithTimeout(ctx, 3*time.Second)
	c, err := ipc.Dial(dialCtx, *pipe)
	done()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Служба не отвечает на канале %s. Она запущена?\n", ipc.Address(*pipe))
		return 1
	}
	defer c.Close()

	var hello proto.HelloResult
	if err := call(ctx, c, proto.MethodHello, proto.HelloParams{Client: "ctl", Version: version, Proto: proto.Version}, &hello); err != nil {
		return fail(err)
	}
	if hello.Proto != proto.Version {
		fmt.Fprintf(os.Stderr, "Внимание: у службы протокол %d, у strelkactl — %d. Обнови Стрелку.\n", hello.Proto, proto.Version)
	}

	switch cmd {
	case "status":
		err = status(ctx, c, hello)
	case "start":
		err = command(ctx, c, proto.MethodVPNStart, *noWait, proto.PhaseOn)
	case "stop":
		err = command(ctx, c, proto.MethodVPNStop, *noWait, proto.PhaseOff)
	case "restart":
		err = command(ctx, c, proto.MethodVPNRestart, *noWait, proto.PhaseOn)
	case "watch":
		err = watch(ctx, c, rest)
	case "call":
		err = rawCall(ctx, c, rest)
	case "subs":
		err = subsCmd(ctx, c, rest)
	case "servers":
		err = serversCmd(ctx, c)
	case "conns":
		err = connsCmd(ctx, c)
	case "select":
		err = selectCmd(ctx, c, rest)
	case "test":
		err = testCmd(ctx, c, rest)
	case "mode":
		err = modeCmd(ctx, c, rest)
	case "rules":
		err = rulesCmd(ctx, c, rest)
	case "set":
		err = setCmd(ctx, c, rest)
	case "import-legacy":
		err = importLegacy(ctx, c, rest)
	case "journal":
		err = journalCmd(ctx, c, rest)
	case "action":
		err = actionCmd(ctx, c, rest)
	case "versions":
		err = versionsCmd(ctx, c, hello)
	case "report":
		err = reportCmd(ctx, c)
	case "guard":
		err = guardCmd(ctx, c)
	case "place":
		err = placeCmd(ctx, c, rest)
	case "check":
		err = checkCmd(ctx, c)
	case "find-legacy":
		err = findLegacyCmd(ctx, c)
	case "retire-legacy":
		err = retireLegacyCmd(ctx, c)
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

func fail(err error) int {
	var perr *proto.Error
	switch {
	case errors.As(err, &perr):
		fmt.Fprintf(os.Stderr, "%s (код %d)\n", perr.Message, perr.Code)
	case errors.Is(err, context.Canceled):
		return 130
	default:
		fmt.Fprintln(os.Stderr, err)
	}
	return 1
}

func call(ctx context.Context, c *ipc.Client, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := c.Call(ctx, method, params, result)
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("служба не ответила на %s за 5 с", method)
	}
	return err
}

var phaseText = map[string]string{
	proto.PhaseOff:        "выключен",
	proto.PhaseStarting:   "включается",
	proto.PhaseOn:         "включён",
	proto.PhaseRecovering: "восстанавливается после сбоя",
	proto.PhaseStopping:   "выключается",
	proto.PhaseFailed:     "не запускается",
}

func status(ctx context.Context, c *ipc.Client, hello proto.HelloResult) error {
	var st proto.State
	if err := call(ctx, c, proto.MethodStateGet, nil, &st); err != nil {
		return err
	}
	var es proto.EngineStatusResult
	if err := call(ctx, c, proto.MethodEngineStatus, nil, &es); err != nil {
		return err
	}

	line := fmt.Sprintf("VPN:     %s (%s)", phaseText[st.VPN.Phase], st.VPN.Phase)
	if st.VPN.Since != nil {
		line += ", с " + st.VPN.Since.Local().Format("15:04:05")
	}
	fmt.Println(line)
	if st.VPN.FailText != nil {
		fmt.Printf("Причина: %s (%s)\n", *st.VPN.FailText, deref(st.VPN.FailReason))
	}
	fmt.Printf("Нужно:   %s\n", st.VPN.Desired)
	fmt.Printf("Режим:   %s (%s)\n", modeText[st.VPN.Mode], st.VPN.Mode)
	server := "не выбран"
	if st.Server.Selected != nil {
		server = *st.Server.Selected
		if st.Server.Ping != nil {
			server += fmt.Sprintf(", %d мс", *st.Server.Ping)
		}
	}
	fmt.Printf("Сервер:  %s (всего серверов: %d, подписок: %d)\n", server, len(st.Servers), len(st.Subs))
	fmt.Printf("Диагноз: %s\n", healthLine(st.Health))
	fmt.Printf("Замок:   %s\n", guardLine(st.Guard))
	fmt.Printf("Место:   %s\n", placeLine(st.Place))
	for _, inc := range st.Incidents {
		fmt.Printf("Инцидент %s [%s]: %s. %s\n", inc.ID, inc.Severity, inc.Title, inc.Body)
	}
	for _, e := range es.Engines {
		if e.Running {
			since := ""
			if e.Since != nil {
				since = ", работает с " + e.Since.Local().Format("15:04:05")
			}
			fmt.Printf("Движок:  %s, pid %d%s, падений: %d\n", e.Name, e.PID, since, e.Restarts)
		} else {
			fmt.Printf("Движок:  %s не запущен\n", e.Name)
		}
		fmt.Printf("         %s\n", e.Path)
	}
	fmt.Printf("Служба:  %s, протокол %d, %s, состояние rev %d\n", hello.Version, hello.Proto, hello.OS, st.Rev)
	return nil
}

// guardLine words State.guard: the setting, whether the filters stand, and
// whether they are what keeps the internet closed right now.
func guardLine(g proto.Guard) string {
	switch {
	case !g.Enabled:
		return "выключен в настройках"
	case g.Blocking:
		return "держит интернет закрытым: VPN не работает"
	case g.Engaged:
		return "включён"
	}
	return "не стоит (VPN выключен или фильтры не поставились)"
}

func guardCmd(ctx context.Context, c *ipc.Client) error {
	var st proto.State
	if err := call(ctx, c, proto.MethodStateGet, nil, &st); err != nil {
		return err
	}
	var gs proto.GuardStatusResult
	if err := call(ctx, c, proto.MethodGuardStatus, nil, &gs); err != nil {
		return err
	}
	fmt.Printf("Замок:           %s\n", guardLine(st.Guard))
	fmt.Printf("Локальная сеть:  %s\n", map[bool]string{true: "разрешена", false: "закрыта"}[st.Guard.AllowLAN])
	fmt.Printf("Фильтров стоит:  %d\n", gs.Filters)
	if gs.TunLUID != 0 {
		fmt.Printf("Адаптер VPN:     разрешён (LUID %#x)\n", gs.TunLUID)
	} else if gs.Engaged {
		fmt.Println("Адаптер VPN:     не найден — через него сейчас ничего не пройдёт")
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return "—"
	}
	return *s
}

// command sends a vpn.* method and follows the state until the phase it
// leads to, so a script can tell success from failure by the exit code.
func command(ctx context.Context, c *ipc.Client, method string, noWait bool, want string) error {
	if noWait {
		return call(ctx, c, method, nil, nil)
	}
	if err := call(ctx, c, proto.MethodStateSubscribe, proto.TopicsParams{Topics: []string{proto.TopicState}}, nil); err != nil {
		return err
	}
	// The subscription delivers the current state first; skip what was
	// there before the command, or "start" on a failed VPN would report the
	// old failure.
	var before proto.State
	if err := next(ctx, c, &before, time.Now().Add(5*time.Second)); err != nil {
		return err
	}
	if err := call(ctx, c, method, nil, nil); err != nil {
		return err
	}
	deadline := time.Now().Add(60 * time.Second)
	lastPhase := ""
	for {
		var st proto.State
		if err := next(ctx, c, &st, deadline); err != nil {
			return err
		}
		if st.Rev <= before.Rev {
			continue
		}
		if st.VPN.Phase != lastPhase {
			lastPhase = st.VPN.Phase
			fmt.Printf("VPN %s (%s)\n", phaseText[st.VPN.Phase], st.VPN.Phase)
		}
		switch st.VPN.Phase {
		case want:
			return nil
		case proto.PhaseFailed:
			return fmt.Errorf("%s (%s)", deref(st.VPN.FailText), deref(st.VPN.FailReason))
		case proto.PhaseOff:
			if want == proto.PhaseOn {
				return errors.New("VPN выключен: кто-то остановил его, пока он включался")
			}
		}
	}
}

// next reads notifications until the next State document.
func next(ctx context.Context, c *ipc.Client, st *proto.State, deadline time.Time) error {
	timeout := time.NewTimer(time.Until(deadline))
	defer timeout.Stop()
	for {
		select {
		case n, ok := <-c.Notifications():
			if !ok {
				return ipc.ErrClosed
			}
			if n.Method != proto.TopicState {
				continue
			}
			return json.Unmarshal(n.Params, st)
		case <-timeout.C:
			return errors.New("служба не прислала новое состояние вовремя")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func watch(ctx context.Context, c *ipc.Client, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.Usage = usage
	withLog := fs.Bool("log", false, "")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return nil
	}
	topics := []string{proto.TopicState, proto.TopicJournal, proto.TopicToast}
	if *withLog {
		topics = append(topics, proto.TopicLog)
	}
	if err := call(ctx, c, proto.MethodStateSubscribe, proto.TopicsParams{Topics: topics}, nil); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-c.Notifications():
			if !ok {
				return ipc.ErrClosed
			}
			if *asJSON {
				fmt.Printf("{\"method\":%q,\"params\":%s}\n", n.Method, n.Params)
				continue
			}
			printNotification(n)
		}
	}
}

var verdictText = map[string]string{
	proto.VerdictOK:               "защищено",
	proto.VerdictSettling:         "подключение",
	proto.VerdictDegraded:         "соединение нестабильно",
	proto.VerdictServerDown:       "сервер не отвечает",
	proto.VerdictAllServersDown:   "серверы не отвечают",
	proto.VerdictNoInternet:       "нет интернета",
	proto.VerdictNoNetwork:        "нет сети",
	proto.VerdictEngineRecovering: "перезапуск",
	proto.VerdictEngineFailed:     "VPN не запускается",
	proto.VerdictHelperDown:       "часть серверов недоступна",
}

func healthLine(h proto.Health) string {
	line := fmt.Sprintf("%s (%s), сеть: %s, интернет: %s", verdictText[h.Verdict], h.Verdict, yesNo(h.Network), yesNo(h.Internet))
	if h.Hint != nil {
		line += ". " + *h.Hint
	}
	return line
}

func yesNo(v bool) string {
	if v {
		return "есть"
	}
	return "нет"
}

// watchSeen is what watch printed last, so that a diagnosis or an incident
// gets a line of its own only when it changes: State arrives for every
// traffic counter too.
var watchSeen struct {
	health    string
	incidents string
}

func printNotification(n ipc.Notification) {
	now := time.Now().Format("15:04:05.000")
	switch n.Method {
	case proto.TopicState:
		var st proto.State
		if json.Unmarshal(n.Params, &st) != nil {
			return
		}
		line := fmt.Sprintf("%s  состояние  rev %-4d phase=%-10s desired=%-3s busy=%s verdict=%s",
			now, st.Rev, st.VPN.Phase, st.VPN.Desired, deref(st.Busy), st.Health.Verdict)
		if st.VPN.FailReason != nil {
			line += " fail=" + *st.VPN.FailReason
		}
		fmt.Println(line)
		if h := healthLine(st.Health); h != watchSeen.health {
			watchSeen.health = h
			fmt.Printf("%s  диагноз    %s\n", now, h)
		}
		var open []string
		for _, inc := range st.Incidents {
			mark := ""
			if inc.Unstable {
				mark = ", нестабильно"
			}
			open = append(open, fmt.Sprintf("%s [%s%s, повторов %d] %s. %s", inc.ID, inc.Severity, mark, inc.Flaps, inc.Title, inc.Body))
		}
		if list := strings.Join(open, "\n"); list != watchSeen.incidents {
			watchSeen.incidents = list
			if len(open) == 0 {
				fmt.Printf("%s  инциденты  открытых нет\n", now)
			}
			for _, o := range open {
				fmt.Printf("%s  инцидент   %s\n", now, o)
			}
		}
	case proto.TopicToast:
		var t proto.Toast
		if json.Unmarshal(n.Params, &t) != nil {
			return
		}
		if t.Close {
			fmt.Printf("%s  УВЕДОМЛЕНИЕ %s: убрать с экрана\n", now, t.ID)
			return
		}
		var buttons []string
		for _, a := range t.Actions {
			buttons = append(buttons, fmt.Sprintf("[%s → %s]", a.Label, a.ID))
		}
		sticky := ""
		if t.Sticky {
			sticky = ", не прячется само"
		}
		fmt.Printf("%s  УВЕДОМЛЕНИЕ %s (%s%s): %s. %s %s\n", now, t.ID, t.Severity, sticky, t.Title, t.Body, strings.Join(buttons, " "))
	case proto.TopicJournal:
		var e proto.JournalEntry
		if json.Unmarshal(n.Params, &e) != nil {
			return
		}
		fmt.Printf("%s  журнал     [%s/%s] %s%s\n", now, e.Cat, e.Level, e.Text, incidentMark(e))
	case proto.TopicLog:
		var l proto.LogLine
		if json.Unmarshal(n.Params, &l) != nil {
			return
		}
		fmt.Printf("%s  %-9s  %s\n", now, l.Source, l.Line)
	}
}

func rawCall(ctx context.Context, c *ipc.Client, args []string) error {
	if len(args) == 0 || len(args) > 2 {
		return errors.New("нужен метод и, если есть, параметры одним JSON: strelkactl call state.subscribe '{\"topics\":[\"state\"]}'")
	}
	var params any
	if len(args) == 2 {
		raw := json.RawMessage(strings.TrimSpace(args[1]))
		if !json.Valid(raw) {
			return errors.New("параметры — не JSON")
		}
		params = raw
	}
	var result json.RawMessage
	// Any method may be called here, the slow ones included.
	if err := callLong(ctx, c, args[0], params, &result); err != nil {
		return err
	}
	var pretty any
	if json.Unmarshal(result, &pretty) != nil {
		fmt.Println(string(result))
		return nil
	}
	out, _ := json.MarshalIndent(pretty, "", "  ")
	fmt.Println(string(out))
	return nil
}

func incidentMark(e proto.JournalEntry) string {
	if e.Incident == nil {
		return ""
	}
	return "  (" + *e.Incident + ")"
}

func journalCmd(ctx context.Context, c *ipc.Client, args []string) error {
	fs := flag.NewFlagSet("journal", flag.ContinueOnError)
	fs.Usage = usage
	n := fs.Int("n", 50, "")
	problems := fs.Bool("problems", false, "")
	incident := fs.String("incident", "", "")
	if err := fs.Parse(args); err != nil {
		return nil
	}
	p := proto.JournalListParams{Limit: *n}
	if *problems || *incident != "" {
		p.Filter = &proto.JournalFilter{Incident: *incident}
		if *problems {
			p.Filter.Levels = []string{proto.LevelProblem, proto.LevelCritical}
		}
	}
	var res proto.JournalListResult
	if err := call(ctx, c, proto.MethodJournalList, p, &res); err != nil {
		return err
	}
	if len(res.Entries) == 0 {
		fmt.Println("Записей нет.")
		return nil
	}
	if res.More {
		fmt.Printf("… более старые записи не показаны (-n %d)\n", *n)
	}
	// The daemon answers newest first; a console reads better the other way.
	for i := len(res.Entries) - 1; i >= 0; i-- {
		e := res.Entries[i]
		fmt.Printf("%s  [%s/%s] %s%s\n", e.T.Local().Format("02.01 15:04:05"), e.Cat, e.Level, e.Text, incidentMark(e))
	}
	return nil
}

func actionCmd(ctx context.Context, c *ipc.Client, args []string) error {
	if len(args) == 0 || len(args) > 2 {
		return errors.New("нужно действие и, если есть, номер инцидента: strelkactl action switch:nl-1 i-20261116-2")
	}
	p := proto.IncidentActionParams{Action: args[0]}
	if len(args) == 2 {
		p.ID = args[1]
	}
	if err := call(ctx, c, proto.MethodIncidentAction, p, nil); err != nil {
		return err
	}
	fmt.Println("Принято.")
	return nil
}
