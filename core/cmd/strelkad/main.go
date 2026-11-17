// strelkad is the Strelka daemon. At this stage it runs in a console only:
//
//	strelkad run --data DIR --engines DIR [--no-tun] [--no-guard] [--pipe NAME]
//	strelkad guard test [--hold 20s] | guard reset
//
// Running under the Windows service manager and systemd arrives with the
// installer.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"strelka/internal/confgen"
	"strelka/internal/daemon"
	"strelka/internal/guard"
	"strelka/internal/ipc"
	"strelka/internal/store"
)

func main() { os.Exit(realMain(os.Args[1:])) }

func realMain(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "run":
		return run(args[1:])
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

  strelkad run --data ПАПКА --engines ПАПКА [--no-tun] [--no-guard] [--pipe ИМЯ]
  strelkad guard test [--hold 20s] [--engines ПАПКА] [--no-lan] [--out ФАЙЛ]
  strelkad guard reset
  strelkad version

  --data     папка данных службы (в разработке — .dev)
  --engines  папка с sing-box и xray
  --no-tun   без сетевого адаптера: вместо него локальный прокси на 127.0.0.1:20808.
             Не требует прав администратора и не мешает VPN, который уже работает
  --no-guard без замка: фильтры сетевого экрана не ставятся, что бы ни стояло в настройках
  --pipe     имя канала; по умолчанию strelka-dev, чтобы не мешать установленной службе

  guard test   поставить фильтры замка на время и снять: проверка самих фильтров.
               ВНИМАНИЕ: на это время интернет закрыт у всего, кроме локальной сети, —
               и у другого VPN тоже. Нужны права администратора
  guard reset  убрать все фильтры Стрелки, даже если служба не запущена
`)
}

func run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Usage = usage
	data := fs.String("data", "", "")
	engines := fs.String("engines", "", "")
	noTUN := fs.Bool("no-tun", false, "")
	noGuard := fs.Bool("no-guard", false, "")
	pipe := fs.String("pipe", ipc.DevName, "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *data == "" || *engines == "" {
		fmt.Fprintln(os.Stderr, "Нужны оба флага: --data и --engines.")
		return 2
	}
	// Absolute paths: the engine is started with the data folder as its
	// working directory, and stray processes are matched by full path.
	paths := store.Paths{Data: abs(*data), Engines: abs(*engines)}

	logf := func(format string, a ...any) {
		fmt.Printf("%s  %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, a...))
	}

	// The channel first: it is the lock that keeps a second daemon from
	// touching the first one's engine.
	l, err := ipc.Listen(*pipe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Канал %s не открылся: %v\nПохоже, служба уже запущена.\n", ipc.Address(*pipe), err)
		return 1
	}
	opts := daemon.Options{Paths: paths, NoTUN: *noTUN, Logf: logf}
	if !*noTUN && !*noGuard {
		// Without the TUN there is nothing for the other programs to go
		// through: a guard would simply cut the machine off.
		opts.Guard = guard.New(logf)
	}
	d, err := daemon.New(opts)
	if err != nil {
		l.Close()
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

	logf("strelkad %s: channel %s, data %s, engines %s", daemon.Version, ipc.Address(*pipe), paths.Data, paths.Engines)
	if *noTUN {
		logf("no TUN: traffic enters through the local proxy on 127.0.0.1:%d (or settings.localProxyPort)", confgen.DevProxyPort)
	}
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
