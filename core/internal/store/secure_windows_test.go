//go:build windows

package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func elevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// The attack from 08-hard-parts.md: a user creates the folder before the
// install and plants a file anyone may read. After Secure the folder is the
// administrators', inherits nothing, and the planted file has lost its own
// entry.
func TestSecureTakesAPlantedFolderBack(t *testing.T) {
	if !elevated() {
		t.Skip("setting the owner to the administrators needs an elevated token")
	}
	dir := filepath.Join(t.TempDir(), "Strelka")
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(dir, "rules", "planted.json")
	if err := os.WriteFile(planted, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	open, err := windows.SecurityDescriptorFromString("D:(A;;FA;;;BU)")
	if err != nil {
		t.Fatal(err)
	}
	openDACL, _, _ := open.DACL()
	if err := windows.SetNamedSecurityInfo(planted, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, openDACL, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sddl(t, planted), ";;;BU)") {
		t.Fatalf("подложенный файл не открылся пользователям: %s", sddl(t, planted))
	}

	if _, err := Secure(dir); err != nil {
		t.Fatal(err)
	}
	root := sddl(t, dir)
	if !strings.HasPrefix(root, "O:BAD:PAI") || strings.Contains(root, "BU") || strings.Contains(root, "AU") {
		t.Errorf("папка: %s", root)
	}
	file := sddl(t, planted)
	if !strings.HasPrefix(file, "O:BA") || strings.Contains(file, "BU") || strings.Contains(file, "(A;;FA") {
		t.Errorf("подложенный файл после Secure: %s", file)
	}

	// The reports folder is the one place users may read.
	reports := filepath.Join(dir, "reports")
	if err := ShareReports(reports); err != nil {
		t.Fatal(err)
	}
	if r := sddl(t, reports); !strings.Contains(r, ";;;IU)") || !strings.Contains(r, ";;;SY)") {
		t.Errorf("reports: %s", r)
	}
	// Running again changes nothing and fails on nothing: install and every
	// start of the service both call it.
	if _, err := Secure(dir); err != nil {
		t.Fatal(err)
	}
}

func sddl(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}
