package legacyfind

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
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

func TestCollectKeepsEveryFolderWithSubscriptionsOnce(t *testing.T) {
	plain := t.TempDir() // a sing-box, but not a Strelka
	downloads := t.TempDir()
	desktop := t.TempDir()
	ours := t.TempDir()
	write := func(dir, text string) {
		if err := os.WriteFile(filepath.Join(dir, Marker), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(downloads, "# a comment\nklex https://example.com/a?token=1\nport https://example.com/b\n")
	write(desktop, string(rune(0xFEFF))+"https://example.com/only")
	write(ours, "https://example.com/ours\n")
	os.Mkdir(filepath.Join(plain, Marker), 0o700) // a folder by that name does not count

	cands := []Candidate{
		{Dir: ""}, {Dir: "relative"},
		{Dir: plain, Running: true},
		{Dir: ours, Running: true},
		{Dir: desktop, Running: true},
		{Dir: downloads, Autostart: true},
		{Dir: desktop + string(filepath.Separator)}, // the same folder spelt again
		{Dir: downloads, Running: true},
	}
	got := Collect(cands, []string{ours})
	want := []Folder{
		{Dir: desktop, Running: true, Subs: []string{""}},
		{Dir: downloads, Running: true, Autostart: true, Subs: []string{"klex", "port"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Collect =\n%+v\nwant\n%+v", got, want)
	}
	// The links never leave the package: only the names do.
	for _, f := range got {
		for _, s := range f.Subs {
			if strings.Contains(s, "://") {
				t.Errorf("a link in the names: %q", s)
			}
		}
	}
	if got := Collect([]Candidate{{Dir: plain}}, nil); len(got) != 0 {
		t.Errorf("nothing to find, got %+v", got)
	}
}

func TestTaskEnabledReadsTheTaskSetting(t *testing.T) {
	on := strings.Replace(taskXML, "<Actions", "<Settings><Enabled>true</Enabled></Settings><Actions", 1)
	off := strings.Replace(taskXML, "<Actions", "<Triggers><LogonTrigger><Enabled>true</Enabled></LogonTrigger></Triggers><Settings><Enabled>false</Enabled></Settings><Actions", 1)
	for name, c := range map[string]struct {
		raw  string
		want bool
	}{
		"no setting is the default": {taskXML, true},
		"on":                        {on, true},
		"off, the trigger aside":    {off, false},
		"not a task":                {"ERROR: The system cannot find the file specified.", false},
	} {
		if got := TaskEnabled([]byte(c.raw)); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// On the development machine this finds the real old Strelka; anywhere it
// must simply not fail.
func TestFindDoesNotFail(t *testing.T) {
	t.Logf("old Strelkas: %+v", FindAll(context.Background()))
}
