//go:build linux

package platform

import (
	"io"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func IsTerminal(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	var attributes syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&attributes)))
	runtime.KeepAlive(file)
	return errno == 0
}
