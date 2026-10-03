//go:build !linux && !windows

package platform

import "os"

func IsTerminal(*os.File) bool {
	return false
}
