//go:build windows

package sandbox

import (
	"errors"
	"syscall"
)

const WSAEADDRINUSE syscall.Errno = 10048

func sshBindUnavailable(err error) bool {
	return errors.Is(err, WSAEADDRINUSE) || errors.Is(err, syscall.WSAEACCES)
}
