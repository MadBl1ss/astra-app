// strelkad is the Strelka daemon:
//
//	strelkad run --data DIR --engines DIR [--no-tun] [--no-guard] [--no-locate] [--pipe NAME]
//	strelkad service install | uninstall | start | stop | status
//	strelkad guard test [--hold 20s] | guard reset
//
// `run` is the same command in a console and under the Windows service
// manager: the installed service is registered with exactly this command
// line, and run notices by itself which of the two it is in.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"strelka/internal/confgen"
	"strelka/internal/daemon"
	"strelka/internal/geo"
	"strelka/internal/guard"
	"strelka/internal/ipc"
	"strelka/internal/store"
)

func main() {
	flush := encodeOutput()
	code := realMain(os.Args[1:])
	flush()
	os.Exit(code)
}

func realMain(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "run":
		return run(args[1:])
	case "service":
		return serviceCmd(args[1:])
	case "guard":
		return guardCmd(args[1:])
	case "version", "--version":
		fmt.Println(daemon.Version)
		return 0
	case "help", "-h", "--help":
		usage()
		return 0
	}
	fmt.Fprintf(os.Stderr, "Неизвестная команда «%s».\n\n", args[0])
	usage()
	return 2
}

func usage() {
	fmt.Fprint(os.Stderr, `Служба Стрелки.

  strelkad run --data ПАПКА --engines ПАПКА [--no-tun] [--no-guard] [--no-locate] [--pipe ИМЯ]
  strelkad service install [--data ПАПКА] [--engines ПАПКА] [--pipe ИМЯ] [--no-tun] [--no-guard] [--no-locate]
  strelkad service uninstall | start | stop | status
  strelkad guard test [--hold 20s] [--engines ПАПКА] [--no-lan] [--out ФАЙЛ]
  strelkad guard reset
  strelkad version

  --data     папка данных службы (в разработке — .dev)
  --engines  папка с sing-box и xray
  --no-tun   без сетевого адаптера: вместо него локальный прокси на 127.0.0.1:20808.
             Не требует прав администратора и не мешает VPN, который уже работает
  --no-guard без замка: фильтры сетевого экрана не ставятся, что бы ни стояло в настройках
  --no-locate не спрашивать геосервис, где компьютер (для глобуса в окне); город можно выбрать вручную
  --pipe     имя канала; по умолчанию strelka-dev, чтобы не мешать установленной службе

  service install    зарегистрировать службу Strelka с автозапуском (или обновить уже
                     зарегистрированную). По умолчанию данные — в %ProgramData%\Strelka,
                     движки — в папке engines рядом с strelkad, канал — strelka.
                     Нужны права администратора
  service uninstall  остановить и удалить службу; данные остаются
  service start | stop  запустить или остановить и дождаться
  service status     состояние службы

  guard test   поставить фильтры замка на время и снять: проверка самих фильтров.
               ВНИМАНИЕ: на это время интернет закрыт у всего, кроме локальной сети, —
               и у другого VPN тоже. Нужны права администратора
  guard reset  убрать все фильтры Стрелки, даже если служба не запущена
`)
}

// runConfig is what `run` is told on its command line.
type runConfig struct {
	paths    store.Paths
	pipe     string
	noTUN    bool
	noGuard  bool
	noLocate bool
}

func run(args []string) int {
	cfg, ok := parseRun(args)
	if !ok {
		return 2
	}
	if underServiceManager() {
		return runService(cfg)
	}
	return runConsole(cfg)
}

func parseRun(args []string) (runConfig, bool) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Usage = usage
	data := fs.String("data", "", "")
	engines := fs.String("engines", "", "")
	noTUN := fs.Bool("no-tun", false, "")
	noGuard := fs.Bool("no-guard", false, "")
	noLocate := fs.Bool("no-locate", false, "")
	pipe := fs.String("pipe", ipc.DevName, "")
	if err := fs.Parse(args); err != nil {
		return runConfig{}, false
	}
	if *data == "" || *engines == "" {
		fmt.Fprintln(os.Stderr, "Нужны оба флага: --data и --engines.")
		return runConfig{}, false
	}
	return runConfig{
		// Absolute paths: the engine is started with the data folder as its
		// working directory (the service's own is System32), and stray
		// processes are matched by full path.
		paths:    store.Paths{Data: abs(*data), Engines: abs(*engines)},
		pipe:     *pipe,
		noTUN:    *noTUN,
		noGuard:  *noGuard,
		noLocate: *noLocate,
	}, true
}

// errChannelTaken means the pipe already exists: another daemon is running,
// or something else holds the name.
var errChannelTaken = errors.New("channel taken")

// open is what the console and the service share: the channel first - it is
// the lock that keeps a second daemon from touching the first one's engine -
// then the daemon itself.
func open(cfg runConfig, logf func(string, ...any)) (*daemon.Daemon, net.Listener, error) {
	l, err := ipc.Listen(cfg.pipe)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s: %v", errChannelTaken, ipc.Address(cfg.pipe), err)
	}
	opts := daemon.Options{Paths: cfg.paths, NoTUN: cfg.noTUN, Logf: logf}
	if !cfg.noTUN && !cfg.noGuard {
		// Without the TUN there is nothing for the other programs to go
		// through: a guard would simply cut the machine off.
		opts.Guard = guard.New(logf)
	}
	if !cfg.noLocate {
		// The one request to a service on the internet the daemon makes by
		// itself, for a picture; tests and offline runs turn it off.
		opts.Locator = geo.New(geo.DefaultURL)
	}
	d, err := daemon.New(opts)
	if err != nil {
		l.Close()
		return nil, nil, err
	}
	logf("strelkad %s: channel %s, data %s, engines %s", daemon.Version, ipc.Address(cfg.pipe), cfg.paths.Data, cfg.paths.Engines)
	if cfg.noTUN {
		logf("no TUN: traffic enters through the local proxy on 127.0.0.1:%d (or settings.localProxyPort)", confgen.DevProxyPort)
	}
	return d, l, nil
}

func runConsole(cfg runConfig) int {
	logf := func(format string, a ...any) {
		fmt.Printf("%s  %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, a...))
	}
	d, l, err := open(cfg, logf)
	if errors.Is(err, errChannelTaken) {
		fmt.Fprintf(os.Stderr, "Канал %s не открылся: %v\nПохоже, служба уже запущена.\n", ipc.Address(cfg.pipe), err)
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Служба не запустилась: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		// Back to the default handler: a second Ctrl+C ends the process at
		// once. The engine still dies with it, the Job Object sees to that.
		stop()
		logf("stop requested; press Ctrl+C again to exit without waiting")
	}()

	if err := d.Run(ctx, l); err != nil {
		fmt.Fprintf(os.Stderr, "Служба остановилась с ошибкой: %v\n", err)
		return 1
	}
	logf("strelkad stopped")
	return 0
}

func abs(path string) string {
	if a, err := filepath.Abs(path); err == nil {
		return a
	}
	return path
}
