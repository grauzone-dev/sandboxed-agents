package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
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
			master, slave := azdoTerminalPair(t)
			original := azdoTerminalAttributes(t, slave)
			fakes := testutil.NewFakePrograms(t)
			owner := "default"
			responses := withIntegrationToolchains(t, sandboxObjectResponses(&owner, true, nil, nil), "azure")
			responses = append(responses, testutil.Response{Stdout: "[]"})
			if !cancelInput {
				responses = append(responses, testutil.Response{WantStdin: token + "\n"})
			}
			fakes.Script("podman", responses...)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLIProcess$", "--", "integrations", "login", "agent01", "azdo")
			command.Env = append(os.Environ(), "SANDBOXED_AGENTS_CLI_FIXTURE=sandbox-host")
			var stdout bytes.Buffer
			command.Stdin, command.Stdout, command.Stderr = slave, &stdout, slave
			captured := make(chan string, 1)
			go func() {
				value, _ := io.ReadAll(master)
				captured <- string(value)
			}()
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			for azdoTerminalAttributes(t, slave).Lflag&syscall.ECHO != 0 {
				if ctx.Err() != nil {
					command.Wait()
					t.Fatal("token prompt never disabled echo")
				}
				time.Sleep(time.Millisecond)
			}
			input := token + "\n"
			if cancelInput {
				input = "\x03"
			}
			if _, err := io.WriteString(master, input); err != nil {
				t.Fatal(err)
			}
			err := command.Wait()
			if ctx.Err() != nil || (!cancelInput && err != nil) {
				t.Fatalf("err=%v timeout=%v", err, ctx.Err())
			}
			if cancelInput {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
					t.Fatalf("canceled login returned %v", err)
				}
			}
			if got := azdoTerminalAttributes(t, slave); got != original {
				t.Fatal("terminal attributes were not restored")
			}
			slave.Close()
			output := <-captured
			if !strings.Contains(output, "Azure DevOps personal access token: ") || strings.Contains(output, token) || stdout.Len() != 0 || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("stdout=%q terminal=%q calls=%v", stdout.String(), output, fakes.Calls("podman"))
			}
		})
	}
}

func TestAzureDevOpsLoginReadsPipedInputEvenWhenOutputIsATerminal(t *testing.T) {
	const token = "offline-pat-49-sensitive"
	terminal := shellTerminal(t)
	fakes := testutil.NewFakePrograms(t)
	owner := "default"
	responses := withIntegrationToolchains(t, sandboxObjectResponses(&owner, true, nil, nil), "azure")
	responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{WantStdin: token + "\n"})
	fakes.Script("podman", responses...)
	command := exec.Command(os.Args[0], "-test.run=^TestCLIProcess$", "--", "integrations", "login", "agent01", "azdo")
	command.Env = append(os.Environ(), "SANDBOXED_AGENTS_CLI_FIXTURE=sandbox-host")
	var stderr bytes.Buffer
	command.Stdin, command.Stdout, command.Stderr = strings.NewReader(token), terminal, &stderr
	if err := command.Run(); err != nil || stderr.Len() != 0 || len(fakes.Calls("podman")) != len(responses) {
		t.Fatalf("err=%v stderr=%q calls=%v", err, stderr.String(), fakes.Calls("podman"))
	}
}

func azdoTerminalPair(t *testing.T) (*os.File, *os.File) {
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
	return master, slave
}

func azdoTerminalAttributes(t *testing.T, file *os.File) syscall.Termios {
	t.Helper()
	var attributes syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&attributes))); errno != 0 {
		t.Fatal(errno)
	}
	return attributes
}
