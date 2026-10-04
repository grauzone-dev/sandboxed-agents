//go:build !windows

package sandbox

import (
	"os"
	"syscall"
)

func openLifecycleLock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
}
