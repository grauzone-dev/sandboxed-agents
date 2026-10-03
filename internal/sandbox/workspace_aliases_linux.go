package sandbox

import (
	"os"
	"syscall"
)

func fileHasAliases(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink > 1
}
