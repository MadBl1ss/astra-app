package daemon

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
	"unicode"

	"strelka/internal/clash"
	"strelka/internal/confgen"
	"strelka/internal/health"
	"strelka/internal/legacyfind"
	"strelka/proto"
)

// The connection check of the window's Settings: one step at a time, from
// the cable to a site through the VPN, so that whoever reads it learns where
// the chain breaks. The steps are proto.Diag*.

// diagSite is fetched through the VPN at the end: the kind of site people
// turn the VPN on for, at an address that answers with an empty page.
const diagSite = "https://www.youtube.com/generate_204"

const (
	diagStepTimeout = 5 * time.Second
	diagSiteTimeout = 8 * time.Second
)

// diagInput is what the check needs to know from the loop, taken once at
// its start: the steps run outside the loop.
type diagInput struct {
	network    bool
	phase      string
	failText   string
	api        *clash.Client
	server     string // id of the selected server, "" when none
	serverName string
	xrayWanted bool
	xrayReady  bool
}

func (d *Daemon) diagRun(c rpcCall) (any, *proto.Error, bool) {
	if d.diagBusy {
		return nil, proto.Errorf(proto.CodeBusy, "Проверка соединения уже идёт."), false
	}
	d.diagBusy = true
	in := diagInput{
		network:    d.st.Health.Network,
		phase:      d.st.VPN.Phase,
		server:     d.prof.Selected,
		xrayWanted: d.xrayWanted,
		xrayReady:  d.xrayReady,
	}
	if d.st.VPN.FailText != nil {
		in.failText = *d.st.VPN.FailText
	}
	if d.live.Load() {
		in.api = d.api.Load()
	}
	if s := d.prof.Server(d.prof.Selected); s != nil {
		in.serverName = s.Name
	} else {
		in.server = ""
	}
	go func() {
		steps := d.runDiag(context.Background(), in, func(s proto.DiagStep) {
			d.srv.Publish(proto.TopicDiag, s)
		})
		d.post(func() {
			d.diagBusy = false
			d.log(proto.CatSystem, proto.LevelInfo, diagSummary(steps))
			c.reply <- rpcReply{result: proto.DiagRunResult{Steps: steps}}
		})
	}()
	return nil, nil, true
}

// diagSummary is the check in one line of the journal.
func diagSummary(steps []proto.DiagStep) string {
	for _, s := range steps {
		if s.Status == proto.DiagFail {
			return "Проверка соединения: " + lowerFirst(s.Text)
		}
	}
	for _, s := range steps {
		if s.Status == proto.DiagSkip {
			return "Проверка соединения: сеть и интернет в порядке, VPN не проверялся — он выключен."
		}
	}
	return "Проверка соединения: всё в порядке."
}

// lowerFirst lets a sentence go on after a colon. An abbreviation like
// "VPN" or "DNS" keeps its capitals.
func lowerFirst(s string) string {
	r := []rune(s)
	if len(r) > 1 && unicode.IsUpper(r[0]) && !unicode.IsUpper(r[1]) {
		r[0] = unicode.ToLower(r[0])
	}
	return string(r)
}

func advice(s string) *string { return &s }

// runDiag takes the steps in order and reports each twice: when it starts
// and when it is over. It returns the outcomes.
func (d *Daemon) runDiag(ctx context.Context, in diagInput, report func(proto.DiagStep)) []proto.DiagStep {
	var out []proto.DiagStep
	step := func(name, running string, run func() proto.DiagStep) proto.DiagStep {
		report(proto.DiagStep{Step: name, Status: proto.DiagRunning, Text: running})
		s := run()
		s.Step = name
		report(s)
		out = append(out, s)
		return s
	}
	skipRest := func(names []string, text string, adv *string) {
		for _, name := range names {
			s := proto.DiagStep{Step: name, Status: proto.DiagSkip, Text: text, Advice: adv}
			report(s)
			out = append(out, s)
		}
	}

	link := step(proto.DiagNetwork, "Смотрю, есть ли сеть…", func() proto.DiagStep {
		if in.network {
			return proto.DiagStep{Status: proto.DiagOK, Text: "Компьютер подключён к сети."}
		}
		return proto.DiagStep{Status: proto.DiagFail, Text: "Компьютер не подключён к сети.",
			Advice: advice("Проверь Wi-Fi или кабель. Без сети VPN работать не может.")}
	})
	if link.Status != proto.DiagOK {
		skipRest([]string{proto.DiagInternet, proto.DiagDNS, proto.DiagEngine, proto.DiagServer, proto.DiagSite},
			"Не проверялось: нет сети.", nil)
		return out
	}

	inet := step(proto.DiagInternet, "Открываю сайт напрямую, мимо VPN…", func() proto.DiagStep {
		ms, err := d.diagDirect(ctx, in.api)
		if err == nil {
			return proto.DiagStep{Status: proto.DiagOK, Text: fmt.Sprintf("Интернет напрямую есть: %d мс.", ms)}
		}
		return proto.DiagStep{Status: proto.DiagFail, Text: "Интернета нет: даже напрямую сайты для проверки не открываются.",
			Advice: advice("Это не VPN. Проверь роутер или подожди, пока заработает провайдер.")}
	})
	if inet.Status != proto.DiagOK {
		skipRest([]string{proto.DiagDNS, proto.DiagEngine, proto.DiagServer, proto.DiagSite},
			"Не проверялось: нет интернета.", nil)
		return out
	}

	step(proto.DiagDNS, "Ищу адрес сайта по имени…", func() proto.DiagStep {
		host := "www.gstatic.com"
		if u, err := url.Parse(health.ServerURLs[0]); err == nil {
			host = u.Hostname()
		}
		if err := d.diagLookup(ctx, host); err == nil {
			return proto.DiagStep{Status: proto.DiagOK, Text: "Адреса сайтов находятся."}
		}
		return proto.DiagStep{Status: proto.DiagFail, Text: "Адреса сайтов не находятся: DNS не отвечает.",
			Advice: advice("Перезапусти VPN. Если не поможет — проверь DNS в настройках сети Windows.")}
	})

	eng := step(proto.DiagEngine, "Проверяю VPN…", func() proto.DiagStep {
		switch in.phase {
		case proto.PhaseOff, proto.PhaseStopping:
			return proto.DiagStep{Status: proto.DiagSkip, Text: "VPN выключен.",
				Advice: advice("Включи VPN и проверь снова: тогда проверятся и сервер, и сайт через VPN.")}
		case proto.PhaseStarting, proto.PhaseRecovering:
			return proto.DiagStep{Status: proto.DiagFail, Text: "VPN ещё подключается.",
				Advice: advice("Подожди несколько секунд и проверь снова.")}
		case proto.PhaseFailed:
			text := "VPN не запустился."
			if in.failText != "" {
				text = "VPN не запустился: " + lowerFirst(in.failText)
			}
			return proto.DiagStep{Status: proto.DiagFail, Text: text,
				Advice: advice("Нажми «Попробовать снова» на Главной. Если не поможет — собери отчёт в Журнале.")}
		}
		if in.api == nil {
			return proto.DiagStep{Status: proto.DiagFail, Text: "Движок VPN не отвечает.",
				Advice: advice("Перезапусти VPN в Настройках. Если не поможет — собери отчёт в Журнале.")}
		}
		cctx, cancel := context.WithTimeout(ctx, diagStepTimeout)
		defer cancel()
		version, err := in.api.Version(cctx)
		if err != nil {
			return proto.DiagStep{Status: proto.DiagFail, Text: "Движок VPN не отвечает.",
				Advice: advice("Перезапусти VPN в Настройках. Если не поможет — собери отчёт в Журнале.")}
		}
		if in.xrayWanted && !in.xrayReady {
			return proto.DiagStep{Status: proto.DiagFail, Text: "Xray не запущен, а выбранный сервер работает через него.",
				Advice: advice("Перезапусти VPN в Настройках. Если не поможет — выбери сервер без Xray.")}
		}
		text := "VPN работает"
		if version != "" {
			text += ", sing-box " + version
		}
		if in.xrayWanted {
			text += ", Xray тоже"
		}
		return proto.DiagStep{Status: proto.DiagOK, Text: text + "."}
	})
	if eng.Status != proto.DiagOK {
		why := "Не проверялось: VPN не работает."
		if eng.Status == proto.DiagSkip {
			why = "Не проверялось: VPN выключен."
		}
		skipRest([]string{proto.DiagServer, proto.DiagSite}, why, nil)
		return out
	}

	srv := step(proto.DiagServer, "Спрашиваю сервер…", func() proto.DiagStep {
		if in.server == "" {
			return proto.DiagStep{Status: proto.DiagFail, Text: "Сервер не выбран.",
				Advice: advice("Выбери сервер на Главной или в Серверах.")}
		}
		var err error
		var ms int
		for _, u := range health.ServerURLs {
			if ms, err = delay(in.api, in.server, u, diagStepTimeout); err == nil {
				return proto.DiagStep{Status: proto.DiagOK, Text: fmt.Sprintf("Сервер «%s» отвечает: %d мс.", in.serverName, ms)}
			}
		}
		return proto.DiagStep{Status: proto.DiagFail, Text: fmt.Sprintf("Сервер «%s» не отвечает.", in.serverName),
			Advice: advice("Выбери другой сервер. Если молчат все — обнови подписки: возможно, кончился срок.")}
	})
	if srv.Status != proto.DiagOK {
		skipRest([]string{proto.DiagSite}, "Не проверялось: сервер не отвечает.", nil)
		return out
	}

	step(proto.DiagSite, "Открываю YouTube через VPN…", func() proto.DiagStep {
		ms, err := delay(in.api, confgen.SelectorTag, diagSite, diagSiteTimeout)
		if err == nil {
			return proto.DiagStep{Status: proto.DiagOK, Text: fmt.Sprintf("YouTube через VPN открывается: %d мс.", ms)}
		}
		return proto.DiagStep{Status: proto.DiagFail, Text: "Через VPN YouTube не открывается, хотя сервер отвечает.",
			Advice: advice("Дальше сервера трафик не идёт: он перегружен или кончилась подписка. Попробуй другой сервер.")}
	})
	return out
}

// diagDirect opens the captive-portal checks past the VPN: through the
// engine's direct outbound when it runs, or by the daemon itself.
func (d *Daemon) diagDirect(ctx context.Context, api *clash.Client) (int, error) {
	var last error
	for _, u := range health.DirectURLs {
		if api != nil {
			ms, err := delay(api, confgen.DirectTag, u, diagStepTimeout)
			if err == nil {
				return ms, nil
			}
			last = err
			continue
		}
		start := time.Now()
		cctx, cancel := context.WithTimeout(ctx, diagStepTimeout)
		err := d.fetch(cctx, u)
		cancel()
		if err == nil {
			return int(time.Since(start).Milliseconds()), nil
		}
		last = err
	}
	return 0, last
}

func (d *Daemon) fetch(ctx context.Context, u string) error {
	if d.opts.Fetch != nil {
		return d.opts.Fetch(ctx, u)
	}
	// Past any proxy of the environment: the question is about the line.
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "strelka")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (d *Daemon) diagLookup(ctx context.Context, host string) error {
	ctx, cancel := context.WithTimeout(ctx, diagStepTimeout)
	defer cancel()
	if d.opts.Lookup != nil {
		return d.opts.Lookup(ctx, host)
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err == nil && len(addrs) == 0 {
		err = fmt.Errorf("no addresses for %s", host)
	}
	return err
}

// legacyDetect looks for an old Strelka off the loop: listing processes and
// asking the task scheduler take a moment.
func (d *Daemon) legacyDetect(c rpcCall) (any, *proto.Error, bool) {
	ours := []string{d.opts.Paths.Data, d.opts.Paths.Engines}
	go func() {
		found := legacyfind.Find(context.Background(), ours...)
		d.post(func() {
			res := proto.LegacyDetectResult{Running: found.Running}
			if found.Dir != "" {
				res.Dir = &found.Dir
			}
			c.reply <- rpcReply{result: res}
		})
	}()
	return nil, nil, true
}
