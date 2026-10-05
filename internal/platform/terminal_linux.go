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

func ReadPassword(file *os.File) (value string, err error) {
	var original syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&original)))
	if errno != 0 {
		return "", errno
	}
	hidden := original
	hidden.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	hidden.Iflag &^= syscall.IXON
	hidden.Cc[syscall.VMIN], hidden.Cc[syscall.VTIME] = 1, 0
	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&hidden)))
	if errno != 0 {
		return "", errno
	}
	defer func() {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&original)))
		runtime.KeepAlive(file)
		if errno != 0 && err == nil {
			err = errno
		}
	}()
	return readPasswordLine(file)
}
