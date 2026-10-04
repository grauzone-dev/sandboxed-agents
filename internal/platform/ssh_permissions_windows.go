//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	sshDescriptorRevision       = 1
	sshFileObject               = 1
	sshOwnerInformation         = 1
	sshDACLInformation          = 4
	sshProtectedDACLInformation = 0x80000000
	unableToMoveReplacement2    = syscall.Errno(1177)
)

func RestrictSSHAccess(path string) (err error) {
	defer func() {
		var pathError *os.PathError
		if err != nil && !errors.As(err, &pathError) {
			err = &os.PathError{Op: "acl", Path: path, Err: err}
		}
	}()
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer token.Close()
	account, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	sid, err := account.User.Sid.String()
	if err != nil {
		return err
	}
	flags := ""
	if info.IsDir() {
		flags = "OICI"
	}
	text, err := syscall.UTF16PtrFromString("O:" + sid + "D:P(A;" + flags + ";FA;;;" + sid + ")")
	if err != nil {
		return err
	}
	advapi := syscall.NewLazyDLL("advapi32.dll")
	var descriptor unsafe.Pointer
	ok, _, callErr := advapi.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW").Call(uintptr(unsafe.Pointer(text)), sshDescriptorRevision, uintptr(unsafe.Pointer(&descriptor)), 0)
	if ok == 0 {
		return callErr
	}
	defer syscall.LocalFree(syscall.Handle(descriptor))
	var owner, acl unsafe.Pointer
	var present, defaulted int32
	ok, _, callErr = advapi.NewProc("GetSecurityDescriptorOwner").Call(uintptr(descriptor), uintptr(unsafe.Pointer(&owner)), uintptr(unsafe.Pointer(&defaulted)))
	if ok == 0 {
		return callErr
	}
	ok, _, callErr = advapi.NewProc("GetSecurityDescriptorDacl").Call(uintptr(descriptor), uintptr(unsafe.Pointer(&present)), uintptr(unsafe.Pointer(&acl)), uintptr(unsafe.Pointer(&defaulted)))
	if ok == 0 {
		return callErr
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	status, _, _ := advapi.NewProc("SetNamedSecurityInfoW").Call(uintptr(unsafe.Pointer(name)), sshFileObject, sshOwnerInformation|sshDACLInformation|sshProtectedDACLInformation, uintptr(owner), 0, uintptr(acl), 0)
	if status != 0 {
		return syscall.Errno(status)
	}
	return nil
}

var replaceFileW = nativeReplaceFileW

func ReplaceFilePreservingDACL(source, target string) error {
	file, err := os.CreateTemp(filepath.Dir(target), ".ssh-config-backup-*")
	if err != nil {
		return err
	}
	backup := file.Name()
	if err := file.Close(); err != nil {
		return errors.Join(err, os.Remove(backup))
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := replaceFileW(source, target, backup); err != nil {
		replaceErr := &os.LinkError{Op: "replace", Old: source, New: target, Err: err}
		if errors.Is(err, unableToMoveReplacement2) {
			if restoreErr := os.Rename(backup, target); restoreErr != nil {
				return &os.PathError{Op: "restore replacement backup", Path: backup, Err: errors.Join(replaceErr, restoreErr)}
			}
			return replaceErr
		}
		if cleanupErr := os.Remove(backup); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			return errors.Join(replaceErr, cleanupErr)
		}
		return replaceErr
	}
	_ = os.Remove(backup)
	return nil
}

func nativeReplaceFileW(source, target, backup string) error {
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	saved, err := syscall.UTF16PtrFromString(backup)
	if err != nil {
		return err
	}
	ok, _, callErr := syscall.NewLazyDLL("kernel32.dll").NewProc("ReplaceFileW").Call(uintptr(unsafe.Pointer(to)), uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(saved)), 0, 0, 0)
	if ok == 0 {
		return callErr
	}
	return nil
}
