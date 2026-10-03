//go:build !windows

package sandbox

import (
	"errors"
	"syscall"
)

func sshBindUnavailable(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.EACCES)
}
