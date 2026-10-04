//go:build !windows

package filelock

import (
	"errors"
	"os"
	"syscall"
)

func TryLock(file *os.File) (func(), error) {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, err
	}
	return func() { syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }, nil
}

func IsBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func IsInterrupted(err error) bool {
	return errors.Is(err, syscall.EINTR)
}
