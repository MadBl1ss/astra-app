package main

import (
	"bufio"
	"io"
	"os"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// outputEnv asks strelkad to print in the system's ANSI code page instead of
// UTF-8. The installer's hooks run strelkad through nsExec, which reads a
// program's output in that code page: the stage 08b check showed every
// Russian line of the service in the uninstaller's log as mojibake.
const outputEnv = "STRELKA_OUTPUT"

// cpUTF8 is CP_UTF8, a system whose ANSI code page is already UTF-8.
const cpUTF8 = 65001

var wideCharToMultiByte = windows.NewLazySystemDLL("kernel32.dll").NewProc("WideCharToMultiByte")

// encodeOutput re-encodes stdout and stderr when outputEnv is "ansi". The
// returned function lets the last line through; call it before exiting.
func encodeOutput() (flush func()) {
	cp := windows.GetACP()
	if os.Getenv(outputEnv) != "ansi" || cp == cpUTF8 {
		return func() {}
	}
	var wg sync.WaitGroup
	var closers []func()
	for _, f := range []**os.File{&os.Stdout, &os.Stderr} {
		real := *f
		r, w, err := os.Pipe()
		if err != nil {
			continue
		}
		*f = w
		closers = append(closers, func() { w.Close() })
		wg.Add(1)
		go func() {
			defer wg.Done()
			copyInCodePage(real, r, cp)
		}()
	}
	return func() {
		for _, c := range closers {
			c()
		}
		wg.Wait()
	}
}

func copyInCodePage(dst io.Writer, src io.Reader, cp uint32) {
	in := bufio.NewReader(src)
	for {
		line, err := in.ReadString('\n')
		if line != "" {
			dst.Write(toCodePage(line, cp))
		}
		if err != nil {
			return
		}
	}
}

// toCodePage converts UTF-8 text; what the code page has no letter for
// becomes "?", as Windows does it.
func toCodePage(s string, cp uint32) []byte {
	u := utf16.Encode([]rune(s))
	if len(u) == 0 {
		return nil
	}
	n, _, _ := wideCharToMultiByte.Call(uintptr(cp), 0, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)), 0, 0, 0, 0)
	if n == 0 {
		return []byte(s)
	}
	out := make([]byte, n)
	n, _, _ = wideCharToMultiByte.Call(uintptr(cp), 0, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)), uintptr(unsafe.Pointer(&out[0])), n, 0, 0)
	return out[:n]
}
