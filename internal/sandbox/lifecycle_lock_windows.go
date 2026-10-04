package sandbox

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	lifecycleLockfileExclusiveLock   = 2
	lifecycleLockfileFailImmediately = 1
	lifecycleErrorLockViolation      = syscall.Errno(33)
)

var lifecycleLockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
var lifecycleUnlockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")

func openLifecycleLock(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

func acquireLifecycleLock(file *os.File) (func(), error) {
	overlapped := new(syscall.Overlapped)
	ok, _, err := lifecycleLockFileEx.Call(file.Fd(), lifecycleLockfileExclusiveLock|lifecycleLockfileFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(overlapped)))
	if ok == 0 {
		return nil, err
	}
	return func() {
		lifecycleUnlockFileEx.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(overlapped)))
	}, nil
}

func lifecycleLockBusy(err error) bool {
	return errors.Is(err, lifecycleErrorLockViolation)
}
