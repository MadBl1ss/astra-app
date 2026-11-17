package legacyfind

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"unicode/utf16"
)

// The task as the old setup registers it, with the folder names changed:
// PowerShell runs a script that lives next to the engine.
const taskXML = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.3" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Actions Context="Author">
    <Exec>
      <Command>C:\Program Files\PowerShell\7\pwsh.exe</Command>
      <Arguments>-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "C:\Users\Тест\Загрузки\sing-box\autostart.ps1"</Arguments>
      <WorkingDirectory>C:\Users\Тест\Загрузки\sing-box</WorkingDirectory>
    </Exec>
  </Actions>
</Task>`

func TestTaskDirsReadsTheFoldersOfTheAction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows paths")
	}
	want := []string{
		`C:\Users\Тест\Загрузки\sing-box`, // the working folder
		`C:\Users\Тест\Загрузки\sing-box`, // the script's
		`C:\Program Files\PowerShell\7`,   // the program's
	}
	if got := TaskDirs([]byte(taskXML)); !reflect.DeepEqual(got, want) {
		t.Errorf("UTF-8: %q", got)
	}
	// schtasks may as well print UTF-16 with its byte order mark.
	u := utf16.Encode([]rune(taskXML))
	raw := []byte{0xFF, 0xFE}
	for _, c := range u {
		raw = append(raw, byte(c), byte(c>>8))
	}
	if got := TaskDirs(raw); !reflect.DeepEqual(got, want) {
		t.Errorf("UTF-16: %q", got)
	}
	if got := TaskDirs([]byte("ERROR: The system cannot find the file specified.")); got != nil {
		t.Errorf("not a task: %q", got)
	}
}

func TestPickTakesTheFirstFolderWithSubscriptions(t *testing.T) {
	plain := t.TempDir() // a sing-box, but not a Strelka
	old := t.TempDir()
	ours := t.TempDir()
	for _, dir := range []string{old, ours} {
		if err := os.WriteFile(filepath.Join(dir, Marker), []byte("https://example.com/sub\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	os.Mkdir(filepath.Join(plain, Marker), 0o700) // a folder by that name does not count

	cands := []Candidate{{Dir: ""}, {Dir: "relative"}, {Dir: plain, Running: true}, {Dir: ours, Running: true}, {Dir: old, Running: true}, {Dir: old}}
	if got := Pick(cands, []string{ours}); got.Dir != old || !got.Running {
		t.Errorf("Pick = %+v, want %q running", got, old)
	}
	if got := Pick([]Candidate{{Dir: plain}}, nil); got.Dir != "" {
		t.Errorf("nothing to find, got %+v", got)
	}
}

// On the development machine this finds the real old Strelka; anywhere it
// must simply not fail.
func TestFindDoesNotFail(t *testing.T) {
	t.Logf("old Strelka: %+v", Find(context.Background()))
}
