//go:build windows

package store

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// dataSDDL is the data folder of the installed service: owned by the
// administrators, open to SYSTEM and administrators only, and protected so
// that nothing is inherited from %ProgramData%, which lets any user create
// and read folders by default.
const dataSDDL = "O:BAD:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// reportsSDDL adds read access for whoever is logged on at the machine to
// the reports folder, on top of what it inherits. The archives there have
// their keys masked (diag.export), and the window shows them in Explorer; it
// never names a place for the daemon to write to.
const reportsSDDL = "D:(A;OICI;GR;;;IU)"

var procTreeResetNamedSecurityInfoW = windows.NewLazySystemDLL("advapi32.dll").NewProc("TreeResetNamedSecurityInfoW")

// Secure makes dir and everything under it the daemon's own: created if
// missing, owner and access list as in dataSDDL, explicit entries on the
// files inside dropped so they inherit from the folder. It runs on every
// start of the service, not only at install: a folder that a user created
// before the install would otherwise stay theirs.
//
// prior is the owner the folder had before, when it existed and was neither
// SYSTEM nor the administrators - either a leftover or an attempt, worth a
// line in the log.
func Secure(dir string) (prior string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	prior = foreignOwner(dir)

	sd, err := windows.SecurityDescriptorFromString(dataSDDL)
	if err != nil {
		return prior, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return prior, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return prior, err
	}
	// Taking ownership of a file someone else owns needs these two; the
	// service (SYSTEM) and an administrator's token have them, disabled.
	enablePrivileges("SeTakeOwnershipPrivilege", "SeRestorePrivilege")

	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return prior, err
	}
	// The tree variant rather than SetNamedSecurityInfo: that one only adds
	// the folder's entries to the children and leaves whatever they had
	// explicitly - and a file planted with "Users: read" would keep it.
	r, _, _ := procTreeResetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(windows.SE_FILE_OBJECT),
		uintptr(windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION),
		uintptr(unsafe.Pointer(owner)),
		0,
		uintptr(unsafe.Pointer(dacl)),
		0,
		0, // KeepExplicit = FALSE
		0, // no progress callback
		1, // ProgressInvokeNever
		0,
	)
	if r != 0 {
		return prior, fmt.Errorf("access list of %s: %w", dir, windows.Errno(r))
	}
	return prior, nil
}

// ShareReports creates the reports folder inside a secured data folder and
// lets interactive users read it.
func ShareReports(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(reportsSDDL)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	// Unprotected: the entries of the data folder still apply, this one is
	// added to them.
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}

// foreignOwner names the owner of path unless it is SYSTEM or the
// administrators, or the path cannot be read.
func foreignOwner(path string) string {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return ""
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return ""
	}
	if owner.IsWellKnown(windows.WinLocalSystemSid) || owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		return ""
	}
	if account, domain, _, err := owner.LookupAccount(""); err == nil {
		if domain != "" {
			return domain + `\` + account
		}
		return account
	}
	return owner.String()
}

// enablePrivileges switches on privileges the process token already holds.
// Failure is not an error: without them Secure still works on everything
// the token may change, and the reset reports what it could not.
func enablePrivileges(names ...string) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return
	}
	defer token.Close()
	for _, n := range names {
		var luid windows.LUID
		if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(n), &luid); err != nil {
			continue
		}
		tp := windows.Tokenprivileges{PrivilegeCount: 1}
		tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
		// A privilege the token lacks is ERROR_NOT_ALL_ASSIGNED; nothing to do.
		_ = windows.AdjustTokenPrivileges(token, false, &tp, 0, nil, nil)
	}
}
