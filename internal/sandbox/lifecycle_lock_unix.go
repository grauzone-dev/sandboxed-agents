//go:build !windows

package sandbox

import (
	"errors"
	"os"
	"syscall"
)

func openLifecycleLock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
}

func acquireLifecycleLock(file *os.File) (func(), error) {
	var err error
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	return func() { syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }, nil
}

func lifecycleLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
