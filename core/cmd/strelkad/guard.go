package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"strelka/internal/confgen"
	"strelka/internal/guard"
	"strelka/internal/store"
)

// maxHold keeps a mistyped --hold from closing the internet for an hour.
const maxHold = 2 * time.Minute

// guardCmd is the kill switch without the daemon: a timed trial of the real
// filters, and the way back to a working network whatever was left behind.
func guardCmd(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "test":
		return guardTest(args[1:])
	case "reset":
		n, err := guard.Reset()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Фильтры не убраны: %v\n", guardWhy(err))
			return 1
		}
		fmt.Printf("Фильтров Стрелки убрано: %d.\n", n)
		return 0
	}
	fmt.Fprintf(os.Stderr, "Неизвестная команда «guard %s».\n\n", args[0])
	usage()
	return 2
}

func guardWhy(err error) string {
	if errors.Is(err, guard.ErrUnsupported) {
		return "на этой системе замка пока нет"
	}
	return err.Error() + " (нужны права администратора)"
}

// guardTest engages the guard for a while and lifts it. Nothing in it waits
// for the network or for the console, and a watchdog ends the process - and
// with it the dynamic session that owns the filters - even if all else hangs.
func guardTest(args []string) int {
	fs := flag.NewFlagSet("guard test", flag.ContinueOnError)
	fs.Usage = usage
	hold := fs.Duration("hold", 20*time.Second, "")
	engines := fs.String("engines", "", "")
	noLAN := fs.Bool("no-lan", false, "")
	out := fs.String("out", "", "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *hold <= 0 || *hold > maxHold {
		fmt.Fprintf(os.Stderr, "--hold должен быть от секунды до %s.\n", maxHold)
		return 2
	}

	// The watchdog first, before anything touches the filter engine.
	time.AfterFunc(*hold+5*time.Second, func() { os.Exit(3) })

	var lines []string
	say := func(format string, a ...any) {
		line := time.Now().Format("15:04:05.000") + "  " + fmt.Sprintf(format, a...)
		fmt.Println(line)
		lines = append(lines, line)
		if *out != "" {
			// Rewritten on every line: the console of whoever started this may
			// be cut off from them for as long as the filters stand.
			os.WriteFile(*out, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		}
	}

	spec := guard.Spec{AllowLAN: !*noLAN}
	if self, err := os.Executable(); err == nil {
		spec.Apps = append(spec.Apps, self)
	}
	if *engines != "" {
		paths := store.Paths{Engines: abs(*engines)}
		spec.Apps = append(spec.Apps, paths.Engine("sing-box"), paths.Engine("xray"))
	}

	g := guard.New(func(format string, a ...any) { say(format, a...) })
	if luid, ok := guard.TunnelLUID(confgen.TUNName); ok {
		g.SetTunnel(luid)
		say("адаптер %s найден, LUID %#x", confgen.TUNName, luid)
	} else {
		say("адаптера %s нет: разрешения для него не будет", confgen.TUNName)
	}
	if err := g.Engage(spec); err != nil {
		say("замок не включился: %s", guardWhy(err))
		return 1
	}
	st := g.Status()
	say("замок включён: фильтров %d, локальная сеть %v. Держу %s", st.Filters, spec.AllowLAN, *hold)
	time.Sleep(*hold)

	err := g.Release()
	say("замок снят (ошибка: %v)", err)
	// Reset doubles as the check: it counts what should not be there.
	left, rerr := guard.Reset()
	switch {
	case rerr != nil:
		say("проверка остатка не удалась: %v", rerr)
		return 1
	case left != 0:
		say("ПОСЛЕ СНЯТИЯ ОСТАВАЛОСЬ ФИЛЬТРОВ: %d — убраны", left)
		return 1
	}
	say("фильтров Стрелки не осталось")
	return 0
}
