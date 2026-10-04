package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"syscall"
	"testing"
	"unsafe"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestGitIdentityForwardsATerminalForMissingValues(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	var unlock int32
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != 0 {
		t.Fatal(err)
	}
	var number uint32
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&number))); err != 0 {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer slave.Close()
	fakes := testutil.NewFakePrograms(t)
	owner := "default"
	responses := sandboxObjectResponses(&owner, true, nil, nil)
	responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{})
	fakes.Script("podman", responses...)
	command := exec.Command(os.Args[0], "-test.run=^TestCLIProcess$", "--", "integrations", "config", "agent01", "git", "identity", "--email=E")
	command.Env = append(os.Environ(), "SANDBOXED_AGENTS_CLI_FIXTURE=sandbox-host")
	command.Stdin, command.Stdout = slave, slave
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil || stderr.Len() != 0 {
		t.Fatalf("err=%v stderr=%q", err, stderr.String())
	}
	calls := fakes.Calls("podman")
	want := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent", "-it", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "integrations", "config", "git", "identity", "--email=E"}
	if len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
		t.Fatalf("calls=%v", calls)
	}
	assertNoSSH(t, fakes)
}
