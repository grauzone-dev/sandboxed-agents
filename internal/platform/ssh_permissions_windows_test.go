//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

func TestRestrictSSHAccessSecuresCurrentOwnedKeyWithoutWriteOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, []byte("private key fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Establish the same current-user ownership as a native ssh-keygen key.
	if err := RestrictSSHAccess(path); err != nil {
		t.Fatal(err)
	}
	before := replacementSecurity(t, path)
	owner, _, found := strings.Cut(before, "D:")
	if !found {
		t.Fatalf("missing fixture owner or DACL: %q", before)
	}
	// OpenSSH's native 0600 creation grants read/write/execute/delete, without
	// WRITE_OWNER. An owner can still replace the DACL through WRITE_DAC.
	setFixtureDACL(t, path, owner+"D:P(A;;0x001301bf;;;"+strings.TrimPrefix(owner, "O:")+")")
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, 0x00080000, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err == nil {
		syscall.CloseHandle(handle)
		t.Fatal("fixture unexpectedly grants WRITE_OWNER")
	}
	if !errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		t.Fatalf("opening fixture for WRITE_OWNER: %v", err)
	}
	if err := RestrictSSHAccess(path); err != nil {
		t.Fatalf("restrict current-owned key without WRITE_OWNER: %v", err)
	}
	// The descriptor includes the unchanged owner, protected inheritance and
	// exactly one full-control ACE for that account, with no other trustees.
	want := owner + "D:P(A;;FA;;;" + strings.TrimPrefix(owner, "O:") + ")"
	if got := replacementSecurity(t, path); got != want {
		t.Fatalf("restricted key security = %q, want %q", got, want)
	}
	assertReplacementContent(t, path, "private key fixture\n")
}

func TestReplaceFilePreservingDACLRestoresOriginalAfterPartialFailure(t *testing.T) {
	source, target := replacementFiles(t)
	setReplacementFixtureDACL(t, target)
	before := replacementSecurity(t, target)
	useReplacementFailure(t, func(source, target, backup string) error {
		if err := os.Rename(target, backup); err != nil {
			return err
		}
		return syscall.Errno(1177)
	})
	err := ReplaceFilePreservingDACL(source, target)
	if !errors.Is(err, syscall.Errno(1177)) {
		t.Fatalf("expected native replacement failure, got %v", err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	assertReplacementContent(t, target, "original config\n")
	if after := replacementSecurity(t, target); after != before {
		t.Fatalf("restored security changed: before %q, after %q", before, after)
	}
	assertNoReplacementBackups(t, target)
}

func TestReplaceFilePreservingDACLRetainsRecoveryCopyWhenRestoreFails(t *testing.T) {
	source, target := replacementFiles(t)
	setReplacementFixtureDACL(t, target)
	before := replacementSecurity(t, target)
	useReplacementFailure(t, func(source, target, backup string) error {
		if err := os.Rename(target, backup); err != nil {
			return err
		}
		if err := os.Mkdir(target, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(target, "occupied"), []byte("cannot replace directory"), 0o600); err != nil {
			return err
		}
		return syscall.Errno(1177)
	})
	err := ReplaceFilePreservingDACL(source, target)
	if !errors.Is(err, syscall.Errno(1177)) {
		t.Fatalf("expected native replacement failure, got %v", err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	paths, globErr := filepath.Glob(filepath.Join(filepath.Dir(target), ".ssh-config-backup-*"))
	if globErr != nil || len(paths) != 1 {
		t.Fatalf("expected one retained recovery copy, got %v: %v", paths, globErr)
	}
	var recoveryError *os.PathError
	if !errors.As(err, &recoveryError) || recoveryError.Path != paths[0] {
		t.Fatalf("recovery error does not identify retained copy %s: %v", paths[0], err)
	}
	assertReplacementContent(t, recoveryError.Path, "original config\n")
	if after := replacementSecurity(t, recoveryError.Path); after != before {
		t.Fatalf("recovery copy security changed: before %q, after %q", before, after)
	}
}

func TestReplaceFilePreservingDACLKeepsOriginalWhenReplacementCannotMove(t *testing.T) {
	source, target := replacementFiles(t)
	setReplacementFixtureDACL(t, target)
	before := replacementSecurity(t, target)
	useReplacementFailure(t, func(source, target, backup string) error {
		if backup == "" {
			if err := os.Remove(target); err != nil {
				return err
			}
		}
		return syscall.Errno(1176)
	})
	err := ReplaceFilePreservingDACL(source, target)
	if !errors.Is(err, syscall.Errno(1176)) {
		t.Fatalf("expected native replacement failure, got %v", err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	assertReplacementContent(t, target, "original config\n")
	if after := replacementSecurity(t, target); after != before {
		t.Fatalf("original security changed: before %q, after %q", before, after)
	}
	assertNoReplacementBackups(t, target)
}

func TestReplaceFilePreservingDACLCommitsDespiteBackupCleanupFailure(t *testing.T) {
	source, target := replacementFiles(t)
	useReplacementFailure(t, func(source, target, backup string) error {
		if err := os.Mkdir(backup, 0o700); err != nil {
			return err
		}
		if err := os.Rename(target, filepath.Join(backup, "config")); err != nil {
			return err
		}
		return os.Rename(source, target)
	})
	if err := ReplaceFilePreservingDACL(source, target); err != nil {
		t.Fatalf("committed replacement reported failure: %v", err)
	}
	assertReplacementContent(t, target, "new config\n")
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".ssh-config-backup-*", "config"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("expected retained original after backup cleanup failure, got %v: %v", paths, err)
	}
	assertReplacementContent(t, paths[0], "original config\n")
}

func TestReplaceFilePreservingDACLCommitsNativeReplacementAndCleansBackup(t *testing.T) {
	source, target := replacementFiles(t)
	setReplacementFixtureDACL(t, target)
	before := replacementSecurity(t, target)
	if err := ReplaceFilePreservingDACL(source, target); err != nil {
		t.Fatal(err)
	}
	assertReplacementContent(t, target, "new config\n")
	if after := replacementSecurity(t, target); after != before {
		t.Fatalf("replacement security changed: before %q, after %q", before, after)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement source remains: %v", err)
	}
	assertNoReplacementBackups(t, target)
}

func useReplacementFailure(t *testing.T, call func(string, string, string) error) {
	t.Helper()
	original := replaceFileW
	replaceFileW = call
	t.Cleanup(func() { replaceFileW = original })
}

func replacementFiles(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "replacement")
	target := filepath.Join(dir, "config")
	if err := os.WriteFile(source, []byte("new config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestrictSSHAccess(source); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("original config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return source, target
}

func assertReplacementContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("content at %s: got %q, want %q", path, content, want)
	}
}

func assertNoReplacementBackups(t *testing.T, target string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".ssh-config-backup-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("unexpected replacement backups: %v", paths)
	}
}

func setReplacementFixtureDACL(t *testing.T, path string) {
	t.Helper()
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	account, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid, err := account.User.Sid.String()
	if err != nil {
		t.Fatal(err)
	}
	setFixtureDACL(t, path, "O:"+sid+"D:P(A;;FA;;;"+sid+")(A;;FR;;;BU)")
}

func setFixtureDACL(t *testing.T, path, sddl string) {
	t.Helper()
	text, err := syscall.UTF16PtrFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	advapi := syscall.NewLazyDLL("advapi32.dll")
	var descriptor unsafe.Pointer
	ok, _, callErr := advapi.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW").Call(uintptr(unsafe.Pointer(text)), 1, uintptr(unsafe.Pointer(&descriptor)), 0)
	if ok == 0 {
		t.Fatal(callErr)
	}
	defer syscall.LocalFree(syscall.Handle(descriptor))
	var acl unsafe.Pointer
	var present, defaulted int32
	ok, _, callErr = advapi.NewProc("GetSecurityDescriptorDacl").Call(uintptr(descriptor), uintptr(unsafe.Pointer(&present)), uintptr(unsafe.Pointer(&acl)), uintptr(unsafe.Pointer(&defaulted)))
	if ok == 0 || present == 0 || acl == nil {
		t.Fatalf("missing fixture DACL: %v", callErr)
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	status, _, _ := advapi.NewProc("SetNamedSecurityInfoW").Call(uintptr(unsafe.Pointer(name)), 1, 0x80000004, 0, 0, uintptr(acl), 0)
	if status != 0 {
		t.Fatal(syscall.Errno(status))
	}
}

func replacementSecurity(t *testing.T, path string) string {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	advapi := syscall.NewLazyDLL("advapi32.dll")
	get := advapi.NewProc("GetFileSecurityW")
	var size uint32
	get.Call(uintptr(unsafe.Pointer(name)), 5, 0, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		t.Fatalf("no security descriptor size for %s", path)
	}
	buffer := make([]byte, size)
	ok, _, callErr := get.Call(uintptr(unsafe.Pointer(name)), 5, uintptr(unsafe.Pointer(&buffer[0])), uintptr(size), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		t.Fatal(callErr)
	}
	var text *uint16
	ok, _, callErr = advapi.NewProc("ConvertSecurityDescriptorToStringSecurityDescriptorW").Call(uintptr(unsafe.Pointer(&buffer[0])), 1, 5, uintptr(unsafe.Pointer(&text)), 0)
	if ok == 0 {
		t.Fatal(callErr)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(text)))
	var length int
	for *(*uint16)(unsafe.Add(unsafe.Pointer(text), length*2)) != 0 {
		length++
	}
	return syscall.UTF16ToString(unsafe.Slice(text, length))
}
