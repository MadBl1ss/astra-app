package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceLogStartsOverWhenFull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strelkad.log")
	l, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 1000)
	for i := 0; i < logLimit/1000+10; i++ {
		l.Printf("%s", line)
	}
	l.Printf("last")
	l.Close()

	cur, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatal("старого файла нет")
	}
	if cur.Size() >= logLimit || old.Size() > logLimit {
		t.Errorf("размеры: %d и %d", cur.Size(), old.Size())
	}
	data, _ := os.ReadFile(path)
	if !strings.HasSuffix(strings.TrimSpace(string(data)), "last") {
		t.Errorf("последняя строка потерялась")
	}
}
