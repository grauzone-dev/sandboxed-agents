package manager_test

import (
	"os"
	"syscall"
	"testing"
)

func sessionTerminal(t *testing.T) *os.File {
	t.Helper()
	kernel := syscall.NewLazyDLL("kernel32.dll")
	allocated, _, allocErr := kernel.NewProc("AllocConsole").Call()
	if allocated != 0 {
		t.Cleanup(func() { kernel.NewProc("FreeConsole").Call() })
	} else if allocErr != syscall.ERROR_ACCESS_DENIED {
		t.Fatal(allocErr)
	}
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close() })
	return input
}
