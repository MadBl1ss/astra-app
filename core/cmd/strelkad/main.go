// strelkad is the Strelka daemon. At this stage it runs in a console only:
//
//	strelkad run --data DIR --engines DIR [--config FILE] [--pipe NAME]
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

	"strelka/internal/daemon"
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

  strelkad run --data ПАПКА --engines ПАПКА [--config ФАЙЛ] [--pipe ИМЯ]
  strelkad version

  --data     папка данных службы (в разработке — .dev)
  --engines  папка с sing-box и xray
  --config   готовый конфиг sing-box; без флага берётся run/config.json из папки данных
  --pipe     имя канала; по умолчанию strelka-dev, чтобы не мешать установленной службе
`)
}

func run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Usage = usage
	data := fs.String("data", "", "")
	engines := fs.String("engines", "", "")
	config := fs.String("config", "", "")
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
	if *config != "" {
		*config = abs(*config)
	}

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
	d, err := daemon.New(daemon.Options{Paths: paths, Config: *config, Logf: logf})
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
