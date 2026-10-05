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
var setConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

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

func ReadPassword(file *os.File) (value string, err error) {
	var original uint32
	result, _, callErr := getConsoleMode.Call(file.Fd(), uintptr(unsafe.Pointer(&original)))
	if result == 0 {
		return "", callErr
	}
	hidden := original &^ uint32(0x0001|0x0002|0x0004)
	result, _, callErr = setConsoleMode.Call(file.Fd(), uintptr(hidden))
	if result == 0 {
		return "", callErr
	}
	defer func() {
		result, _, callErr := setConsoleMode.Call(file.Fd(), uintptr(original))
		runtime.KeepAlive(file)
		if result == 0 && err == nil {
			err = callErr
		}
	}()
	return readPasswordLine(file)
}
