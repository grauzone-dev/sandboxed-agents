//go:build !linux

package sandbox

import "os"

func fileHasAliases(os.FileInfo) bool { return false }
