//go:build !linux && !windows

package platform

import (
	"io"
	"os"
)

func IsTerminal(io.Reader) bool {
	return false
}

func ReadPassword(*os.File) (string, error) {
	return "", os.ErrInvalid
}
