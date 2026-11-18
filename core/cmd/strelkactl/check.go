package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"strelka/internal/ipc"
	"strelka/proto"
)

var stepNames = map[string]string{
	proto.DiagNetwork:  "Сеть",
	proto.DiagInternet: "Интернет напрямую",
	proto.DiagDNS:      "DNS",
	proto.DiagEngine:   "VPN",
	proto.DiagServer:   "Сервер",
	proto.DiagSite:     "Сайт через VPN",
}

var stepMarks = map[string]string{
	proto.DiagOK:   "✓",
	proto.DiagFail: "✗",
	proto.DiagSkip: "–",
}

func printStep(s proto.DiagStep) {
	fmt.Printf("%s %-18s %s\n", stepMarks[s.Status], stepNames[s.Step], s.Text)
	if s.Advice != nil {
		fmt.Printf("  %-18s %s\n", "", *s.Advice)
	}
}

// checkCmd runs the connection check and prints each step as it ends.
func checkCmd(ctx context.Context, c *ipc.Client) error {
	if err := call(ctx, c, proto.MethodStateSubscribe, proto.TopicsParams{Topics: []string{proto.TopicDiag}}, nil); err != nil {
		return err
	}
	done := make(chan error, 1)
	var res proto.DiagRunResult
	go func() { done <- callLong(ctx, c, proto.MethodDiagRun, nil, &res) }()
	printed := 0
	for {
		select {
		case n := <-c.Notifications():
			if n.Method != proto.TopicDiag {
				continue
			}
			var s proto.DiagStep
			if json.Unmarshal(n.Params, &s) == nil && s.Status != proto.DiagRunning {
				printStep(s)
				printed++
			}
		case err := <-done:
			if err != nil {
				return err
			}
			// Steps the notifications have not brought yet.
			for _, s := range res.Steps[min(printed, len(res.Steps)):] {
				printStep(s)
			}
			return nil
		}
	}
}

func findLegacyCmd(ctx context.Context, c *ipc.Client) error {
	var res proto.LegacyDetectResult
	if err := callLong(ctx, c, proto.MethodLegacyDetect, nil, &res); err != nil {
		return err
	}
	if len(res.Found) == 0 {
		fmt.Println("Старая Стрелка не найдена.")
		return nil
	}
	for _, f := range res.Found {
		var notes []string
		if f.Running {
			notes = append(notes, "запущена")
		}
		if f.Autostart {
			notes = append(notes, "запускается при входе")
		}
		names := strings.Join(f.Subs, ", ")
		if len(f.Subs) == 1 && f.Subs[0] == "" {
			names = "одна без названия"
		}
		notes = append(notes, fmt.Sprintf("подписки: %s", names))
		fmt.Println(f.Dir, "("+strings.Join(notes, "; ")+")")
	}
	return nil
}

func retireLegacyCmd(ctx context.Context, c *ipc.Client) error {
	var res proto.LegacyRetireResult
	if err := callLong(ctx, c, proto.MethodLegacyRetire, nil, &res); err != nil {
		return err
	}
	task := "выключена"
	if res.Task == proto.LegacyTaskAbsent {
		task = "не найдена"
	}
	fmt.Printf("Старая Стрелка выключена: остановлено программ — %d, задача планировщика %s.\n", res.Stopped, task)
	return nil
}
