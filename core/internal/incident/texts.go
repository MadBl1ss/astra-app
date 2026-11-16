package incident

import (
	"fmt"
	"strings"
	"time"

	"strelka/internal/health"
	"strelka/proto"
)

// Every word the user reads about an incident is in this file: titles,
// descriptions, buttons, journal lines. They answer three questions - what
// happened, what Strelka has done about it, what can be done - and address
// the user as "ты".

const (
	labelOpen       = "Открыть"
	labelUpdateSubs = "Обновить подписки"
	labelRetry      = "Попробовать снова"
	labelRestart    = "Перезапустить VPN"
)

func labelSwitch(name string) string { return "Переключить на " + name }

// hintText words a guess. It always starts with "похоже": a log line is not
// a proof.
func hintText(h health.Hint) string {
	switch h {
	case health.HintUnreachable:
		return "Похоже, сервер выключен или его порт закрыт."
	case health.HintKeys:
		return "Похоже, сервер отклонил подключение: ключи подписки устарели. Обнови подписку."
	case health.HintAuth:
		return "Похоже, подписка истекла или ключ отозван."
	case health.HintBlocked:
		return "Похоже на блокировку протокола провайдером."
	case health.HintExpired:
		return "Похоже, подписка закончилась."
	}
	return ""
}

// HintText is the wording of a hint for State.health.hint; empty for none.
func HintText(h health.Hint) string { return hintText(h) }

func join(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

// describe gives an incident its title and body for what is known now.
func describe(kind string, in Input, unstable bool) (title, body string) {
	server := in.Server.title()
	if unstable {
		title = "Соединение нестабильно"
		switch kind {
		case proto.VerdictServerDown, proto.VerdictDegraded:
			body = fmt.Sprintf("Сервер %s то отвечает, то нет.", server)
		case proto.VerdictAllServersDown:
			body = "Серверы то отвечают, то нет."
		case proto.VerdictNoInternet:
			body = "Интернет то есть, то пропадает."
		case proto.VerdictNoNetwork:
			body = "Сеть то есть, то пропадает."
		default:
			body = "Проблема то появляется, то пропадает."
		}
		return title, join(body, hintText(in.Hint))
	}
	switch kind {
	case proto.VerdictServerDown:
		if in.Moved != nil {
			return fmt.Sprintf("%s не отвечает, включён %s", server, in.Moved.title()),
				"Запасной сервер включился сам. Вернуться на прежний можно в списке серверов."
		}
		return fmt.Sprintf("Сервер %s не отвечает", server),
			join("Интернет есть, другие серверы работают.", hintText(in.Hint))
	case proto.VerdictAllServersDown:
		return "Серверы не отвечают", join("Интернет есть, но ни один сервер не ответил.", hintText(in.Hint))
	case proto.VerdictNoInternet:
		return "Нет интернета", "Сеть подключена, но интернет не отвечает. VPN заработает сам, когда он вернётся."
	case proto.VerdictNoNetwork:
		return "Нет сети", "Компьютер не подключён к сети. VPN заработает сам, когда она появится."
	case proto.VerdictDegraded:
		return "Соединение нестабильно", fmt.Sprintf("Сервер %s отвечает через раз или слишком медленно.", server)
	case proto.VerdictEngineFailed:
		body = in.FailText
		if body == "" {
			body = "Причина неизвестна. Подробности — в журнале."
		}
		return "VPN не запускается", body
	case proto.VerdictHelperDown:
		return "Часть серверов недоступна",
			"Не работает помощник Xray: серверы, которые подключаются через него, молчат."
	}
	return "Проблема с VPN", ""
}

// actions are the buttons of an incident's toast.
func actions(kind string, in Input) []proto.ToastAction {
	open := proto.ToastAction{ID: proto.ActionOpen, Label: labelOpen}
	switch kind {
	case proto.VerdictServerDown:
		if in.Moved == nil && in.Switch != nil {
			return []proto.ToastAction{{ID: proto.ActionSwitch + in.Switch.ID, Label: labelSwitch(in.Switch.title())}, open}
		}
	case proto.VerdictAllServersDown:
		return []proto.ToastAction{{ID: proto.ActionUpdateSubs, Label: labelUpdateSubs}, open}
	case proto.VerdictEngineFailed:
		return []proto.ToastAction{{ID: proto.ActionRetry, Label: labelRetry}, open}
	case proto.VerdictHelperDown:
		return []proto.ToastAction{{ID: proto.ActionRetry, Label: labelRestart}, open}
	}
	return []proto.ToastAction{open}
}

// sentence makes "Title" and "Body." one journal line.
func sentence(title, body string) string {
	if body == "" {
		return title + "."
	}
	return title + ". " + body
}

func reopenText(title string, flaps int) string {
	return fmt.Sprintf("%s — опять. Повторов за последнее время: %d.", title, flaps)
}

// endText is the journal line, and the toast body, of an incident's end.
func endText(kind string, server Named, unstable bool, lasted time.Duration) string {
	d := span(lasted)
	if unstable {
		return fmt.Sprintf("Соединение снова стабильно. Нестабильно было %s.", d)
	}
	switch kind {
	case proto.VerdictServerDown:
		return fmt.Sprintf("Сервер %s снова отвечает. Не работало %s.", server.title(), d)
	case proto.VerdictAllServersDown:
		return fmt.Sprintf("Серверы снова отвечают. Не работало %s.", d)
	case proto.VerdictNoInternet:
		return fmt.Sprintf("Интернет вернулся. Его не было %s.", d)
	case proto.VerdictNoNetwork:
		return fmt.Sprintf("Сеть вернулась. Её не было %s.", d)
	case proto.VerdictDegraded:
		return fmt.Sprintf("Соединение снова стабильно. Нестабильно было %s.", d)
	case proto.VerdictEngineFailed:
		return fmt.Sprintf("VPN запустился. Не работал %s.", d)
	case proto.VerdictHelperDown:
		return fmt.Sprintf("Помощник Xray снова работает. Не работал %s.", d)
	}
	return fmt.Sprintf("Снова работает. Не работало %s.", d)
}

const titleBack = "Снова работает"

func mootText(title string) string {
	return title + ": VPN выключен, проблема снята."
}

// span words a duration the way people say it: "40 с", "2 мин", "1 ч 5 мин".
func span(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d с", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%d мин", int((d+30*time.Second)/time.Minute))
	}
	h, m := int(d/time.Hour), int(d%time.Hour/time.Minute)
	if m == 0 {
		return fmt.Sprintf("%d ч", h)
	}
	return fmt.Sprintf("%d ч %d мин", h, m)
}

// ---- subscriptions --------------------------------------------------------

func days(n int) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return fmt.Sprintf("%d день", n)
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return fmt.Sprintf("%d дня", n)
	}
	return fmt.Sprintf("%d дней", n)
}

func gigabytes(n int64) string {
	return strings.Replace(fmt.Sprintf("%.1f ГБ", float64(n)/(1<<30)), ".", ",", 1)
}

func describeSub(kind string, s SubStatus, now time.Time) (title, body string) {
	switch kind {
	case proto.IncidentSubExpiring:
		left := "меньше суток"
		if d := int(s.ExpireAt.Sub(now) / (24 * time.Hour)); d >= 1 {
			left = days(d)
		}
		return fmt.Sprintf("Подписка «%s» скоро закончится", s.Name),
			fmt.Sprintf("Осталось %s. Продли её у провайдера, иначе серверы перестанут работать.", left)
	case proto.IncidentSubExpired:
		return fmt.Sprintf("Подписка «%s» закончилась", s.Name),
			fmt.Sprintf("Срок вышел %s. Её серверы могут не работать, пока подписка не продлена.", s.ExpireAt.Local().Format("02.01.2006"))
	case proto.IncidentSubTraffic:
		return fmt.Sprintf("В подписке «%s» закончился трафик", s.Name),
			fmt.Sprintf("Использовано %s из %s. Её серверы могут не работать, пока трафик не пополнен.", gigabytes(*s.Used), gigabytes(*s.Total))
	}
	return "", ""
}

func subBackText(name string) string {
	return fmt.Sprintf("Подписка «%s» снова действует.", name)
}

// ---- things that are news only at the "all" level --------------------------

// Connected is the toast of a VPN that has just come up and answered.
func Connected(server Named, ping int) proto.Toast {
	body := "Сервер " + server.title() + "."
	if ping > 0 {
		body = fmt.Sprintf("Сервер %s, пинг %d мс.", server.title(), ping)
	}
	return proto.Toast{ID: "e-connected", Severity: proto.SeverityInfo, Title: "VPN подключён", Body: body, Actions: []proto.ToastAction{}}
}

// SubsRefreshed is the toast of subscriptions the daemon updated by itself.
func SubsRefreshed(servers int) proto.Toast {
	return proto.Toast{
		ID: "e-subs", Severity: proto.SeverityInfo, Title: "Подписки обновлены",
		Body: fmt.Sprintf("Серверов теперь: %d.", servers), Actions: []proto.ToastAction{},
	}
}
