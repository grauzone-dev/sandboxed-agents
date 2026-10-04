//go:build !windows

package platform

import "os"

func RestrictSSHAccess(path string) error {
	return nil
}

func ReplaceSSHFile(source, target string) error {
	return os.Rename(source, target)
}
