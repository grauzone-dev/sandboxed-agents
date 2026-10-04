package cli_test

import (
	"os"
	"runtime"
	"testing"
)

func withInteractiveTerminal(t *testing.T, action func()) {
	t.Helper()
	input := shellTerminal(t)
	output := input
	if runtime.GOOS == "windows" {
		var err error
		output, err = os.OpenFile("CONOUT$", os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { output.Close() })
	}
	originalInput, originalOutput := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = input, output
	defer func() { os.Stdin, os.Stdout = originalInput, originalOutput }()
	action()
}
