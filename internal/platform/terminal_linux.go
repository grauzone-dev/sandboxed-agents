package platform

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func IsTerminal(file *os.File) bool {
	var attributes syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&attributes)))
	runtime.KeepAlive(file)
	return errno == 0
}
