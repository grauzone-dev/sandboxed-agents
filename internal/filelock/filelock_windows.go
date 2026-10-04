package filelock

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	lockfileExclusiveLock   = 2
	lockfileFailImmediately = 1
	errorLockViolation      = syscall.Errno(33)
)

var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
var unlockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")

func TryLock(file *os.File) (func(), error) {
	overlapped := new(syscall.Overlapped)
	ok, _, err := lockFileEx.Call(file.Fd(), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(overlapped)))
	if ok == 0 {
		return nil, err
	}
	return func() {
		unlockFileEx.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(overlapped)))
	}, nil
}

func IsBusy(err error) bool {
	return errors.Is(err, errorLockViolation)
}

func IsInterrupted(error) bool {
	return false
}
