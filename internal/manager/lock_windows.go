package manager

import (
	"context"
	"os"
	"syscall"
	"time"
	"unsafe"
)

const (
	lockfileExclusiveLock   = 2
	lockfileFailImmediately = 1
	errorLockViolation      = syscall.Errno(33)
)

var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
var unlockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")

func lockManager(ctx context.Context, path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	overlapped := new(syscall.Overlapped)
	for {
		ok, _, err := lockFileEx.Call(file.Fd(), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(overlapped)))
		if ok != 0 {
			return func() { unlockFileEx.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(overlapped))); file.Close() }, nil
		}
		if err != errorLockViolation {
			file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
