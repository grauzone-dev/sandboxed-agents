//go:build !linux && !windows

package platform

import "io"

func IsTerminal(io.Reader) bool {
	return false
}
