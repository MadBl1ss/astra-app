package procs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListHasThisTestAndNoDuplicates(t *testing.T) {
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	want := filepath.Base(self)
	seen := map[string]bool{}
	found := false
	for _, p := range got {
		key := strings.ToLower(p.Name)
		if seen[key] {
			t.Errorf("%s в списке дважды", p.Name)
		}
		seen[key] = true
		if p.Name == "" || p.Path == "" {
			t.Errorf("пустая запись: %+v", p)
		}
		if strings.EqualFold(p.Name, want) {
			found = true
		}
	}
	// On Linux as root the test binary is filtered out with the system's own.
	if !found && os.Getuid() != 0 {
		t.Errorf("этого теста (%s) нет среди %d программ", want, len(got))
	}
}
