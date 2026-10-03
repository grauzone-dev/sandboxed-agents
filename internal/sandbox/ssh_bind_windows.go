package sandbox

import (
	"errors"
	"syscall"
)

func sshBindUnavailable(err error) bool {
	return errors.Is(err, syscall.Errno(10048)) || errors.Is(err, syscall.Errno(10013))
}
