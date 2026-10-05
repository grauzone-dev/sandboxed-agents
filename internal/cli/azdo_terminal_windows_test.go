package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestAzureDevOpsLoginPromptsWithoutEchoAndRestoresTheTerminal(t *testing.T) {
	const token = "offline-pat-49-sensitive"
	for _, cancelInput := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelInput), func(t *testing.T) {
			input := shellTerminal(t)
			kernel := syscall.NewLazyDLL("kernel32.dll")
			mode := func() uint32 {
				var result uint32
				if ok, _, err := kernel.NewProc("GetConsoleMode").Call(input.Fd(), uintptr(unsafe.Pointer(&result))); ok == 0 {
					t.Fatal(err)
				}
				return result
			}
			original := mode()
			fakes := testutil.NewFakePrograms(t)
			owner := "default"
			responses := append(healthyWindowsPodman()[1:3], withIntegrationToolchains(t, sandboxObjectResponses(&owner, true, nil, nil), "azure")...)
			responses = append(responses, testutil.Response{Stdout: "[]"})
			if !cancelInput {
				responses = append(responses, testutil.Response{WantStdin: token + "\n"})
			}
			fakes.Script("podman", responses...)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLIProcess$", "--", "integrations", "login", "agent01", "azdo")
			command.Env = append(os.Environ(), "SANDBOXED_AGENTS_CLI_FIXTURE=windows")
			var stdout, stderr bytes.Buffer
			command.Stdin, command.Stdout, command.Stderr = input, &stdout, &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			for mode()&0x0004 != 0 {
				if ctx.Err() != nil {
					command.Wait()
					t.Fatal("token prompt never disabled echo")
				}
				time.Sleep(time.Millisecond)
			}
			value := token + "\r"
			if cancelInput {
				value = "\x03"
			}
			for _, character := range value {
				record := struct {
					EventType, Padding                              uint16
					KeyDown                                         int32
					RepeatCount, VirtualKey, VirtualScan, Character uint16
					ControlKey                                      uint32
				}{EventType: 1, KeyDown: 1, RepeatCount: 1, Character: uint16(character)}
				var written uint32
				if ok, _, err := kernel.NewProc("WriteConsoleInputW").Call(input.Fd(), uintptr(unsafe.Pointer(&record)), 1, uintptr(unsafe.Pointer(&written))); ok == 0 || written != 1 {
					t.Fatalf("console input: %v, written=%d", err, written)
				}
			}
			err := command.Wait()
			if ctx.Err() != nil || (!cancelInput && err != nil) {
				t.Fatalf("err=%v timeout=%v stderr=%q", err, ctx.Err(), stderr.String())
			}
			if cancelInput {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
					t.Fatalf("canceled login returned %v", err)
				}
			}
			if mode() != original {
				t.Fatal("console mode was not restored")
			}
			if !strings.Contains(stderr.String(), "Azure DevOps personal access token: ") || strings.Contains(stderr.String(), token) || stdout.Len() != 0 || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("stdout=%q stderr=%q calls=%v", stdout.String(), stderr.String(), fakes.Calls("podman"))
			}
		})
	}
}
