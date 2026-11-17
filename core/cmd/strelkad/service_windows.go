//go:build windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"strelka/internal/daemon"
	"strelka/internal/ipc"
	"strelka/internal/store"
)

const (
	serviceName    = "Strelka"
	serviceDisplay = "Стрелка"
	serviceDesc    = "VPN Стрелки: держит sing-box и Xray, замок и диагнозы. Окно и значок в трее говорят с ней по каналу strelka."
)

// Power broadcast event types (PBT_*) that arrive with svc.PowerEvent.
const (
	pbtSuspend         = 4
	pbtResumeSuspend   = 7
	pbtResumeAutomatic = 18
)

func underServiceManager() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// ---- running under the service manager ------------------------------------

func runService(cfg runConfig) int {
	// The data folder is secured before anything in it is read: profile and
	// settings are what the config is built from, and the config runs as
	// SYSTEM. Every start, not only at install (see store.Secure).
	prior, secureErr := store.Secure(cfg.paths.Data)
	if err := cfg.paths.Ensure(); err != nil {
		return 1
	}
	log, err := openLog(cfg.paths.EngineLog("strelkad"))
	if err != nil {
		return 1
	}
	defer log.Close()
	if secureErr != nil {
		log.Printf("securing the data folder: %v", secureErr)
	}
	if prior != "" {
		log.Printf("the data folder belonged to %s before; it is the administrators' now. A leftover, or someone got there first", prior)
	}
	if err := store.ShareReports(cfg.paths.ReportsDir()); err != nil {
		log.Printf("the reports folder: %v", err)
	}

	err = svc.Run(serviceName, &service{cfg: cfg, log: log})
	if err != nil {
		log.Printf("service manager: %v", err)
		return 1
	}
	return 0
}

type service struct {
	cfg runConfig
	log *logFile
}

// Execute is the service's life between the manager's start and stop. A
// non-zero exit code is a failure to the manager, and with
// SetRecoveryActionsOnNonCrashFailures it restarts the service.
func (s *service) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	// At once: the manager gives a starting service 30 s, and New below
	// clears stray engines and reads the profile.
	status <- svc.Status{State: svc.StartPending, WaitHint: 30_000}

	// A shared console is what lets the engine stop cleanly: CTRL_BREAK only
	// reaches processes on the sender's console, and a service has none.
	if err := attachHiddenConsole(); err != nil {
		s.log.Printf("no console for the engines (%v): they will be stopped by force", err)
	}

	d, l, err := open(s.cfg, s.log.Printf)
	if errors.Is(err, errChannelTaken) {
		s.log.Printf("%v (another daemon is running, or a program holds the name to pose as the service)", err)
		return true, 2
	}
	if err != nil {
		s.log.Printf("daemon did not start: %v", err)
		return true, 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, l) }()

	running := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPowerEvent}
	status <- running
	s.log.Printf("running as the %s service", serviceName)

	for {
		select {
		case err := <-done:
			// Nobody asked it to stop.
			s.log.Printf("daemon ended by itself: %v", err)
			return true, 3
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				// desired is left as it is: after a reboot the VPN comes up
				// again if it was on.
				s.log.Printf("stop requested (%s)", cmdName(c.Cmd))
				cancel()
				return s.waitStopped(done, status)
			case svc.PowerEvent:
				switch c.EventType {
				case pbtSuspend:
					s.log.Printf("the system is going to sleep")
				case pbtResumeAutomatic, pbtResumeSuspend:
					d.Woke()
				}
			}
		}
	}
}

// waitStopped keeps the manager informed while the engines and the guard
// come down: a stop pending without progress for longer than its hint
// counts as hung.
func (s *service) waitStopped(done <-chan error, status chan<- svc.Status) (bool, uint32) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	st := svc.Status{State: svc.StopPending, WaitHint: 5_000}
	for {
		st.CheckPoint++
		status <- st
		select {
		case err := <-done:
			if err != nil {
				s.log.Printf("daemon stopped with an error: %v", err)
				return true, 1
			}
			s.log.Printf("strelkad stopped")
			return false, 0
		case <-tick.C:
		}
	}
}

func cmdName(c svc.Cmd) string {
	if c == svc.Shutdown {
		return "shutdown"
	}
	return "stop"
}

var (
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procAllocConsole     = kernel32.NewProc("AllocConsole")
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	procShowWindow       = windows.NewLazySystemDLL("user32.dll").NewProc("ShowWindow")
)

// attachHiddenConsole gives the service a console the engines inherit. In
// session 0 nobody would see its window, but it is hidden all the same.
func attachHiddenConsole() error {
	if r, _, err := procAllocConsole.Call(); r == 0 {
		return err
	}
	if hwnd, _, _ := procGetConsoleWindow.Call(); hwnd != 0 {
		procShowWindow.Call(hwnd, 0) // SW_HIDE
	}
	return nil
}

// ---- strelkad service ... ---------------------------------------------------

func serviceCmd(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "install":
		return serviceInstall(args[1:])
	case "uninstall":
		return serviceUninstall()
	case "start":
		return serviceStart()
	case "stop":
		return serviceStop()
	case "status":
		return serviceStatus()
	}
	fmt.Fprintf(os.Stderr, "Неизвестная команда «service %s».\n\n", args[0])
	usage()
	return 2
}

// connect opens the service manager with the rights install and uninstall
// need; the message for a missing administrator is the one people hit.
func connect() (*mgr.Mgr, bool) {
	m, err := mgr.Connect()
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			fmt.Fprintln(os.Stderr, "Нужны права администратора.")
		} else {
			fmt.Fprintf(os.Stderr, "Диспетчер служб недоступен: %v\n", err)
		}
		return nil, false
	}
	return m, true
}

func serviceInstall(args []string) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Не найден путь к strelkad: %v\n", err)
		return 1
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	exe = abs(exe)

	fs := flag.NewFlagSet("service install", flag.ContinueOnError)
	fs.Usage = usage
	data := fs.String("data", filepath.Join(programData(), "Strelka"), "")
	engines := fs.String("engines", filepath.Join(filepath.Dir(exe), "engines"), "")
	pipe := fs.String("pipe", ipc.DefaultName, "")
	noTUN := fs.Bool("no-tun", false, "")
	noGuard := fs.Bool("no-guard", false, "")
	noLocate := fs.Bool("no-locate", false, "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	runArgs := []string{"run", "--data", abs(*data), "--engines", abs(*engines), "--pipe", *pipe}
	for _, f := range []struct {
		on   bool
		flag string
	}{{*noTUN, "--no-tun"}, {*noGuard, "--no-guard"}, {*noLocate, "--no-locate"}} {
		if f.on {
			runArgs = append(runArgs, f.flag)
		}
	}

	m, ok := connect()
	if !ok {
		return 1
	}
	defer m.Disconnect()

	conf := mgr.Config{
		ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		DisplayName:  serviceDisplay,
		Description:  serviceDesc,
		// The guard needs the Base Filtering Engine. An automatic service
		// may otherwise start before it, and the VPN would come up after a
		// reboot without the guard.
		Dependencies: []string{"BFE"},
	}
	s, err := m.OpenService(serviceName)
	if err == nil {
		// An update runs install again: the same service, new path and
		// settings, never a failure because it already exists.
		conf.BinaryPathName = commandLine(exe, runArgs)
		err = s.UpdateConfig(conf)
		if err != nil {
			s.Close()
			fmt.Fprintf(os.Stderr, "Служба не обновилась: %v\n", err)
			return 1
		}
		fmt.Println("Служба Strelka обновлена.")
	} else {
		s, err = m.CreateService(serviceName, exe, conf, runArgs...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Служба не создалась: %v\n", err)
			return 1
		}
		fmt.Println("Служба Strelka создана.")
	}
	defer s.Close()

	// Restart after 1, 5 and 30 s; the count starts over after a day without
	// failures.
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}
	if err := s.SetRecoveryActions(actions, uint32((24 * time.Hour).Seconds())); err != nil {
		fmt.Fprintf(os.Stderr, "Перезапуск после сбоя не настроился: %v\n", err)
		return 1
	}
	// Without this the manager restarts a service that crashed, but not one
	// that stopped with an error code - which is how Execute reports most
	// failures.
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		fmt.Fprintf(os.Stderr, "Перезапуск после сбоя не настроился: %v\n", err)
		return 1
	}

	// The folder exists and is the administrators' before the service ever
	// runs; the service checks it again on every start.
	prior, err := store.Secure(abs(*data))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Права на папку данных не выставились: %v\n", err)
		return 1
	}
	if prior != "" {
		fmt.Printf("Папка данных принадлежала %s; теперь она администраторов.\n", prior)
	}
	fmt.Printf("Данные: %s\nДвижки: %s\nКанал: %s\n", abs(*data), abs(*engines), ipc.Address(*pipe))
	return 0
}

// commandLine is what CreateService makes of exe and args; UpdateConfig takes
// the whole line.
func commandLine(exe string, args []string) string {
	s := syscall.EscapeArg(exe)
	for _, a := range args {
		s += " " + syscall.EscapeArg(a)
	}
	return s
}

func programData() string {
	if p := os.Getenv("ProgramData"); p != "" {
		return p
	}
	return `C:\ProgramData`
}

func serviceUninstall() int {
	m, ok := connect()
	if !ok {
		return 1
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Println("Службы Strelka нет.")
		return 0
	}
	defer s.Close()
	if code := stopAndWait(s); code != 0 {
		return code
	}
	if err := s.Delete(); err != nil {
		fmt.Fprintf(os.Stderr, "Служба не удалилась: %v\n", err)
		return 1
	}
	fmt.Println("Служба Strelka удалена. Данные остались на месте.")
	return 0
}

func serviceStart() int {
	m, ok := connect()
	if !ok {
		return 1
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Службы Strelka нет: сначала strelkad service install.")
		return 1
	}
	defer s.Close()
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		fmt.Fprintf(os.Stderr, "Служба не запустилась: %v\n", err)
		return 1
	}
	st, err := waitState(s, svc.Running, 40*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Служба не запустилась: %v\n", err)
		return 1
	}
	fmt.Printf("Служба Strelka запущена (PID %d).\n", st.ProcessId)
	return 0
}

func serviceStop() int {
	m, ok := connect()
	if !ok {
		return 1
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Println("Службы Strelka нет.")
		return 0
	}
	defer s.Close()
	return stopAndWait(s)
}

// stopAndWait stops the service and returns once its process is gone, so
// that an installer may overwrite strelkad.exe right after.
func stopAndWait(s *mgr.Service) int {
	st, err := s.Query()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Состояние службы не прочиталось: %v\n", err)
		return 1
	}
	if st.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			fmt.Fprintf(os.Stderr, "Служба не остановилась: %v\n", err)
			return 1
		}
		if _, err := waitState(s, svc.Stopped, 60*time.Second); err != nil {
			fmt.Fprintf(os.Stderr, "Служба не остановилась: %v\n", err)
			return 1
		}
	}
	// Stopped is reported a moment before the process exits; the file stays
	// locked until it does.
	if st.ProcessId != 0 {
		if h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, st.ProcessId); err == nil {
			windows.WaitForSingleObject(h, 10_000)
			windows.CloseHandle(h)
		}
	}
	fmt.Println("Служба Strelka остановлена.")
	return 0
}

func waitState(s *mgr.Service, want svc.State, limit time.Duration) (svc.Status, error) {
	deadline := time.Now().Add(limit)
	for {
		st, err := s.Query()
		if err != nil {
			return st, err
		}
		if st.State == want {
			return st, nil
		}
		if want == svc.Running && st.State == svc.Stopped {
			return st, fmt.Errorf("служба остановилась сразу (код %d, свой код %d); причина — в logs\\strelkad.log папки данных",
				st.Win32ExitCode, st.ServiceSpecificExitCode)
		}
		if time.Now().After(deadline) {
			return st, fmt.Errorf("за %s так и не дождались", limit)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func serviceStatus() int {
	// mgr.Connect asks for every right, so this too wants an administrator;
	// `sc query Strelka` answers without one.
	m, ok := connect()
	if !ok {
		return 1
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Println("Службы Strelka нет.")
		return 1
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Состояние службы не прочиталось: %v\n", err)
		return 1
	}
	conf, _ := s.Config()
	names := map[svc.State]string{
		svc.Stopped: "остановлена", svc.StartPending: "запускается", svc.StopPending: "останавливается",
		svc.Running: "работает", svc.ContinuePending: "продолжается", svc.PausePending: "приостанавливается", svc.Paused: "приостановлена",
	}
	fmt.Printf("Служба Strelka: %s", names[st.State])
	if st.ProcessId != 0 {
		fmt.Printf(", PID %d", st.ProcessId)
	}
	fmt.Printf("\nКоманда: %s\nВерсия этого strelkad: %s\n", conf.BinaryPathName, daemon.Version)
	return 0
}
