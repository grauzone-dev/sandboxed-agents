//go:build !linux && !windows

package sandbox

import "os"

func fileHasAliases(string, os.FileInfo) (bool, error) { return false, nil }
