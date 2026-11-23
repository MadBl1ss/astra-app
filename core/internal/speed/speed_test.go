package speed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"strelka/internal/clash"
	"strelka/internal/confgen"
)

// paced is a body that gives chunk bytes every gap, after a slow start of
// slowFor at a quarter of that pace, and ends after total bytes.
type paced struct {
	chunk  int
	gap    time.Duration
	slow   time.Duration
	total  int64
	given  int64
	start  time.Time
	closed chan struct{}
}

func newPaced(chunk int, gap, slow time.Duration, total int64) *paced {
	return &paced{chunk: chunk, gap: gap, slow: slow, total: total, closed: make(chan struct{})}
}

func (p *paced) Read(b []byte) (int, error) {
	if p.start.IsZero() {
		p.start = time.Now()
	}
	if p.given >= p.total {
		return 0, io.EOF
	}
	gap := p.gap
	if time.Since(p.start) < p.slow {
		gap *= 4
	}
	select {
	case <-time.After(gap):
	case <-p.closed:
		return 0, errors.New("closed")
	}
	n := min(p.chunk, len(b), int(p.total-p.given))
	p.given += int64(n)
	return n, nil
}

func (p *paced) Close() error {
	select {
	case <-p.closed:
	default:
		close(p.closed)
	}
	return nil
}

// 32 KiB every 5 ms is about 52 Mbit/s; the first 200 ms go at a quarter of
// that. The settled number leaves the slow start out.
func TestMeasureLeavesTheSlowStartOut(t *testing.T) {
	body := newPaced(32<<10, 5*time.Millisecond, 200*time.Millisecond, 1<<30)
	var samples []Sample
	res, err := Measure(context.Background(), body, Limits{Bytes: 1 << 30, Time: 2 * time.Second}, 50*time.Millisecond, func(s Sample) {
		samples = append(samples, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	full := Mbps(32<<10, 5*time.Millisecond)
	avg := Mbps(res.Bytes, res.Elapsed)
	t.Logf("установившаяся %.1f, средняя %.1f, без задержек было бы %.1f Мбит/с", res.Mbps, avg, full)
	if res.Mbps <= avg {
		t.Errorf("медленное начало не отброшено: %.1f Мбит/с при средней %.1f", res.Mbps, avg)
	}
	if res.Elapsed < 1900*time.Millisecond || res.Elapsed > 2500*time.Millisecond {
		t.Errorf("скачивание шло %v, а предел 2 с", res.Elapsed)
	}
	if len(samples) < 10 {
		t.Fatalf("за две секунды пришло %d отсчётов", len(samples))
	}
	for i := 1; i < len(samples); i++ {
		if samples[i].Bytes < samples[i-1].Bytes || samples[i].Elapsed < samples[i-1].Elapsed {
			t.Fatalf("отсчёты идут назад: %+v -> %+v", samples[i-1], samples[i])
		}
	}
	if last := samples[len(samples)-1]; last.Mbps <= 0 {
		t.Errorf("последний отсчёт без скорости: %+v", last)
	}
}

func TestMeasureStopsAtTheByteLimit(t *testing.T) {
	body := newPaced(64<<10, time.Millisecond, 0, 1<<30)
	res, err := Measure(context.Background(), body, Limits{Bytes: 1 << 20, Time: 10 * time.Second}, 50*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes < 1<<20 || res.Bytes > 1<<20+64<<10 {
		t.Errorf("скачано %d байт при пределе %d", res.Bytes, 1<<20)
	}
	if res.Mbps <= 0 {
		t.Errorf("скорость не посчитана: %+v", res)
	}
	select {
	case <-body.closed:
	default:
		t.Error("Measure не закрыл ответ")
	}
}

func TestMeasureEndsWithTheFile(t *testing.T) {
	body := newPaced(64<<10, time.Millisecond, 0, 300<<10)
	res, err := Measure(context.Background(), body, Default, 50*time.Millisecond, nil)
	if err != nil || res.Bytes != 300<<10 || res.Mbps <= 0 {
		t.Errorf("короткий файл: %+v, %v", res, err)
	}
}

func TestMeasureWithNothing(t *testing.T) {
	_, err := Measure(context.Background(), io.NopCloser(strings.NewReader("")), Default, 50*time.Millisecond, nil)
	if !errors.Is(err, ErrNoData) {
		t.Errorf("пустой ответ: %v", err)
	}
	// A stop while nothing came is the stop, not a result.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	body := newPaced(1, time.Hour, 0, 1)
	if _, err := Measure(ctx, body, Default, 10*time.Millisecond, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("остановка до первого байта: %v", err)
	}
}

// Through the real engine: the gate takes the server its user name says and
// nothing without the password. The "server" is a direct outbound to a test
// server on this machine. Skipped without the engine.
func TestGateWithTheEngine(t *testing.T) {
	name := "sing-box"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	box, _ := filepath.Abs(filepath.Join("..", "..", "..", "engines", name))
	if _, err := os.Stat(box); err != nil {
		t.Skipf("нет %s: проверка движком пропущена", box)
	}
	const size = 3 << 20
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(size))
		w.Write(make([]byte, size))
	}))
	defer site.Close()

	servers := []confgen.Server{
		{ID: "t-LOCAL", Outbound: json.RawMessage(`{"type":"direct","tag":"t-LOCAL"}`)},
		{ID: "t-BLOCK", Outbound: json.RawMessage(`{"type":"block","tag":"t-BLOCK"}`)},
	}
	gate := &confgen.Gate{Port: freePort(t), Password: "made-up-secret"}
	controller := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	raw, err := confgen.BuildProbe(servers, controller, "api-secret", gate)
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	cfg := filepath.Join(data, "probe.json")
	if err := os.WriteFile(cfg, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := confgen.Check(context.Background(), box, cfg, data); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(box, "run", "-c", cfg, "-D", data, "--disable-color")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	api := clash.New(controller, "api-secret")
	for deadline := time.Now().Add(10 * time.Second); ; {
		if _, err := api.Version(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("движок не поднялся")
		}
		time.Sleep(50 * time.Millisecond)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", gate.Port)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	body, err := Open(ctx, addr, confgen.GateUser("t-LOCAL"), gate.Password, site.URL)
	if err != nil {
		t.Fatalf("через t-LOCAL: %v", err)
	}
	res, err := Measure(ctx, body, Limits{Bytes: 1 << 30, Time: 5 * time.Second}, 50*time.Millisecond, nil)
	if err != nil || res.Bytes != size {
		t.Errorf("через t-LOCAL скачано %d из %d: %v", res.Bytes, size, err)
	}
	if _, err := Open(ctx, addr, confgen.GateUser("t-BLOCK"), gate.Password, site.URL); err == nil {
		t.Error("t-BLOCK пропустил соединение: шлюз взял не тот сервер")
	}
	if _, err := Open(ctx, addr, confgen.GateUser("t-LOCAL"), "wrong", site.URL); err == nil || !strings.Contains(err.Error(), "407") {
		t.Errorf("шлюз пустил с чужим паролем: %v", err)
	}
	if _, err := Open(ctx, addr, confgen.GateUser("t-NOBODY"), gate.Password, site.URL); err == nil {
		t.Error("шлюз пустил незнакомого пользователя")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
