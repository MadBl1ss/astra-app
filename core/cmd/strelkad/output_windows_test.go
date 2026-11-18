package main

import (
	"bytes"
	"strings"
	"testing"
)

// nsExec reads the hooks' output in the ANSI code page; 1251 is the one of
// a Russian Windows, and what the installer's log needs to show Cyrillic.
func TestOutputInCodePage1251(t *testing.T) {
	got := toCodePage("Служба Strelka остановлена.\n", 1251)
	want := []byte{0xD1, 0xEB, 0xF3, 0xE6, 0xE1, 0xE0, ' ', 'S', 't', 'r', 'e', 'l', 'k', 'a', ' '}
	if !bytes.HasPrefix(got, want) || !bytes.HasSuffix(got, []byte(".\n")) {
		t.Errorf("toCodePage = % x", got)
	}
	// A letter the code page lacks is not dropped silently.
	if got := toCodePage("ӿ", 1251); len(got) != 1 {
		t.Errorf("unmappable: % x", got)
	}
	var out bytes.Buffer
	copyInCodePage(&out, strings.NewReader("a\nб"), 1251)
	if !bytes.Equal(out.Bytes(), []byte{'a', '\n', 0xE1}) {
		t.Errorf("copyInCodePage = % x", out.Bytes())
	}
}
