package main

import (
	"context"
	"encoding/json"
	"fmt"

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
	if res.Dir == nil {
		fmt.Println("Старая Стрелка не найдена.")
		return nil
	}
	if res.Running {
		fmt.Println(*res.Dir, "(запущена)")
		return nil
	}
	fmt.Println(*res.Dir)
	return nil
}
