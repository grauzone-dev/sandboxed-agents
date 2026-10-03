package platform

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var getConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleMode")

func IsTerminal(file *os.File) bool {
	var mode uint32
	result, _, _ := getConsoleMode.Call(file.Fd(), uintptr(unsafe.Pointer(&mode)))
	runtime.KeepAlive(file)
	return result != 0
}
