package legacyfind

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"strelka/proto"
)

// The test binary doubles as the old Strelka's programs: copied under their
// names and started with this variable, it only waits to be stopped.
const sleepEnv = "LEGACYFIND_TEST_SLEEP"

func TestMain(m *testing.M) {
	if os.Getenv(sleepEnv) == "1" {
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeProgram copies the test binary into dir as name and starts it. The
// channel closes when the program has ended.
func fakeProgram(t *testing.T, dir, name string) <-chan struct{} {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	src, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	dst.Close()
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), sleepEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-done
	})
	return done
}

func exited(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	case <-time.After(3 * time.Second):
		return false
	}
}

// Retire stops what runs from the old Strelka's folder and nothing else,
// and turns its task off without deleting it. The task here is a test one:
// the real "sing-box VPN autostart" may be the developer's working VPN.
func TestRetireStopsTheOldProgramsAndTurnsTheTaskOff(t *testing.T) {
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil || session == 0 {
		t.Skip("the old Strelka is looked for in user sessions; this test runs in session 0")
	}
	old := t.TempDir()
	other := t.TempDir() // a sing-box that is not the old Strelka's
	if err := os.WriteFile(filepath.Join(old, Marker), []byte("https://example.com/sub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tray := fakeProgram(t, old, "sing-box-tray.exe")
	engine := fakeProgram(t, old, "sing-box.exe")
	stranger := fakeProgram(t, other, "sing-box.exe")

	ctx := context.Background()
	task := fmt.Sprintf("strelka-test-retire-%d", os.Getpid())
	if out, err := schtasks(ctx, "/create", "/tn", task, "/tr", `cmd.exe /c exit 0`, "/sc", "once", "/st", "23:59", "/f"); err != nil {
		t.Skipf("cannot register a test task here: %v %s", err, strings.TrimSpace(string(toUTF8(out))))
	}
	t.Cleanup(func() { schtasks(context.Background(), "/delete", "/tn", task, "/f") })

	// The folders as FindAll reports them: the running programs found it.
	found := Collect(candidatesFor(t, old, other), nil)
	if len(found) != 1 || found[0].Dir != filepath.Clean(old) || !found[0].Running {
		t.Fatalf("found %+v", found)
	}

	stopped, state, err := retire(ctx, []string{found[0].Dir}, task)
	if err != nil {
		t.Fatal(err)
	}
	if stopped != 2 || state != proto.LegacyTaskDisabled {
		t.Errorf("stopped %d, task %q", stopped, state)
	}
	if !exited(tray) || !exited(engine) {
		t.Error("the old Strelka's programs are still running")
	}
	if exited(stranger) {
		t.Error("a sing-box from another folder was stopped")
	}
	raw, err := schtasks(ctx, "/query", "/tn", task, "/xml", "ONE")
	if err != nil {
		t.Fatalf("the task is gone, it was to be kept: %v", err)
	}
	if TaskEnabled(raw) {
		t.Error("the task is still on")
	}

	// Again: nothing left to stop, the task stays off, no error.
	if stopped, state, err := retire(ctx, []string{found[0].Dir}, task); err != nil || stopped != 0 || state != proto.LegacyTaskDisabled {
		t.Errorf("second call: %d %q %v", stopped, state, err)
	}
	if _, state, err := retire(ctx, nil, task+"-absent"); err != nil || state != proto.LegacyTaskAbsent {
		t.Errorf("no task: %q %v", state, err)
	}
}

// candidatesFor is candidates() narrowed to the test's folders, so that the
// developer's real old Strelka does not enter the test.
func candidatesFor(t *testing.T, dirs ...string) []Candidate {
	t.Helper()
	keep := map[string]bool{}
	for _, d := range dirs {
		keep[key(d)] = true
	}
	var out []Candidate
	for _, c := range candidates(context.Background()) {
		if keep[key(c.Dir)] {
			out = append(out, c)
		}
	}
	return out
}
