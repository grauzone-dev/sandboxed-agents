//go:build windows

package platform

import (
	"io"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var getConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleMode")

func IsTerminal(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	var mode uint32
	result, _, _ := getConsoleMode.Call(file.Fd(), uintptr(unsafe.Pointer(&mode)))
	runtime.KeepAlive(file)
	return result != 0
}
