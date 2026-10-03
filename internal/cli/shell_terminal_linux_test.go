package cli_test

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func shellTerminal(t *testing.T) *os.File {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close() })
	var number uint32
	var unlocked int32
	for _, request := range []struct {
		code uintptr
		data unsafe.Pointer
	}{
		{syscall.TIOCSPTLCK, unsafe.Pointer(&unlocked)},
		{syscall.TIOCGPTN, unsafe.Pointer(&number)},
	} {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), request.code, uintptr(request.data)); errno != 0 {
			t.Fatal(errno)
		}
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	return slave
}
