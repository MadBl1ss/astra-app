package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strelka/internal/engine"
	"strelka/internal/engine/enginetest"
	"strelka/internal/ipc"
	"strelka/internal/store"
	"strelka/proto"
)

const envDaemonArgs = "STRELKA_TEST_DAEMON"

// The test binary plays three parts: the test, the daemon (when given
// arguments through the environment) and the engine (when it finds itself
// named sing-box, the way the daemon starts it from the engines folder).
func TestMain(m *testing.M) {
	if exe, err := os.Executable(); err == nil && strings.HasPrefix(filepath.Base(exe), "sing-box") {
		os.Setenv(enginetest.EnvScript, "ok")
		enginetest.Main()
	}
	if args := os.Getenv(envDaemonArgs); args != "" {
		os.Exit(realMain(strings.Split(args, "\x1f")))
	}
	os.Exit(m.Run())
}

// The whole binary, end to end: it comes up with desired = on, starts the
// engine, and on Ctrl+C stops it politely and exits with code 0, leaving
// desired as it was.
func TestInterruptStopsTheEngineAndExitsCleanly(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	paths := store.Paths{Data: t.TempDir(), Engines: t.TempDir()}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	copyFile(t, self, paths.Engine("sing-box"))
	// One made-up server: without any there is nothing to switch on. The
	// stand-in engine also answers "sing-box check" (see enginetest.Main).
	profile := `{"servers":[{"id":"t-DE","name":"Germany","protocol":"vless","via":"sing-box","host":"de.example.com","port":443,
		"outbound":{"type":"vless","tag":"t-DE","server":"de.example.com","server_port":443,"uuid":"11111111-1111-4111-8111-111111111111"},"sub":""}],
		"selected":"t-DE","mode":"rule"}`
	if err := os.WriteFile(paths.ProfileFile(), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.DesiredFile(), []byte(`{"desired":"on"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pipe := fmt.Sprintf("strelka-test-main-%d", os.Getpid())

	// --no-guard: this daemon switches the VPN on for real, and in an
	// administrator's console a real kill switch would close the machine's
	// internet for the length of the test.

	daemon := exec.Command(self)
	daemon.Env = append(os.Environ(),
		envDaemonArgs+"="+strings.Join([]string{"run", "--data", paths.Data, "--engines", paths.Engines, "--pipe", pipe, "--no-guard"}, "\x1f"),
		enginetest.EnvDir+"="+paths.Data, // inherited by the engine
	)
	var out bytes.Buffer
	daemon.Stdout, daemon.Stderr = &out, &out
	ownGroup(daemon)
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- daemon.Wait() }()
	defer func() {
		daemon.Process.Kill()
		if t.Failed() {
			t.Logf("вывод службы:\n%s", out.String())
		}
	}()

	c := dialRetry(t, pipe)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.Call(ctx, proto.MethodStateSubscribe, proto.TopicsParams{Topics: []string{proto.TopicState}}, nil); err != nil {
		t.Fatal(err)
	}
	for on := false; !on; {
		select {
		case n, ok := <-c.Notifications():
			if !ok {
				t.Fatal("служба закрыла соединение, не дойдя до on")
			}
			var st proto.State
			if err := json.Unmarshal(n.Params, &st); err != nil {
				t.Fatal(err)
			}
			if st.VPN.Phase == proto.PhaseFailed {
				t.Fatalf("служба не подняла движок: %v", *st.VPN.FailText)
			}
			on = st.VPN.Phase == proto.PhaseOn
		case <-ctx.Done():
			t.Fatal("служба с desired = on не включила VPN сама")
		}
	}
	var es proto.EngineStatusResult
	if err := c.Call(ctx, proto.MethodEngineStatus, nil, &es); err != nil {
		t.Fatal(err)
	}
	enginePID := es.Engines[0].PID
	if !engine.Alive(enginePID) {
		t.Fatalf("движок (pid %d) не работает", enginePID)
	}

	if err := interrupt(daemon.Process.Pid); err != nil {
		t.Skipf("в этом окружении службе нельзя послать Ctrl+C (%v)", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("служба вышла с ошибкой: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("служба не завершилась после Ctrl+C")
	}
	if !strings.Contains(out.String(), "strelkad stopped") {
		t.Errorf("служба не дошла до конца остановки")
	}
	for deadline := time.Now().Add(5 * time.Second); engine.Alive(enginePID); {
		if time.Now().After(deadline) {
			t.Fatalf("движок (pid %d) пережил службу", enginePID)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(paths.Data, "graceful")); err != nil {
		t.Errorf("движок снят принудительно, а не остановлен мягко")
	}
	if strings.Contains(out.String(), "by force") {
		t.Errorf("служба сообщила о принудительной остановке")
	}
	saved, err := store.LoadJSON(paths.DesiredFile(), map[string]string{})
	if err != nil || saved["desired"] != proto.DesiredOn {
		t.Errorf("после Ctrl+C desired = %q (%v): остановка службы не должна его менять", saved["desired"], err)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"нет-такой-команды"}, {"run"}, {"run", "--data", "x"}} {
		if code := realMain(args); code != 2 {
			t.Errorf("strelkad %v: код %d, жду 2", args, code)
		}
	}
	if code := realMain([]string{"version"}); code != 0 {
		t.Errorf("strelkad version: код %d", code)
	}
}

func dialRetry(t *testing.T, pipe string) *ipc.Client {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, err := ipc.Dial(ctx, pipe)
		cancel()
		if err == nil {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("служба не открыла канал: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}
