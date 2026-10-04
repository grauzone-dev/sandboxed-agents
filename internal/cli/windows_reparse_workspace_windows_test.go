package cli_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestWindowsUpAcceptsWorkspaceContainingANonAliasReparseFile(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	root := workspaceFixture(t)
	configureWindowsWorkspacePaths(t, root)
	workspace := filepath.Join(root, "project")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "opaque-metadata")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	setWindowsWorkspaceNonAliasReparseTag(t, path)
	info, err := os.Lstat(path)
	if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected a non-alias reparse file: info=%v error=%v", info, err)
	}
	if _, err := os.Readlink(path); err == nil {
		t.Fatal("non-alias reparse file unexpectedly has a link target")
	}
	responses := append(healthyWindowsPodman(), upObjectResponses(nil, false, nil, nil)[1:]...)
	responses = append(responses, make([]testutil.Response, 5)...)
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "up", "agent01", workspace)
	if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
	}
	calls := fakes.Calls("podman")
	if len(calls) < 9 {
		t.Fatalf("no sandbox creation: %v", calls)
	}
	operations := windowsOperationCalls(t, calls[7:], "podman-machine-default")
	create := operations[len(operations)-2].Args
	mounts := windowsWorkspaceMounts(t, create)
	want := []string{"type=bind", "source=" + windowsWorkspaceSource(t, workspace), "target=/workspace"}
	if create[0] != "create" || len(mounts) != 3 || !reflect.DeepEqual(mounts[0], want) {
		t.Fatalf("create=%v mounts=%v want workspace=%v", create, mounts, want)
	}
	assertNoSSH(t, fakes)
}

func setWindowsWorkspaceNonAliasReparseTag(t *testing.T, path string) {
	t.Helper()
	const tag uint32 = 0x42
	const setReparsePoint uint32 = 0x000900a4
	const deleteReparsePoint uint32 = 0x000900ac
	var buffer [24]byte
	binary.LittleEndian.PutUint32(buffer[:4], tag)
	copy(buffer[8:], []byte{0xe1, 0x8e, 0x5e, 0x1c, 0xb7, 0xe2, 0xf8, 0x43, 0x94, 0xb5, 0x27, 0xc9, 0xc3, 0x88, 0x49, 0x95})
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	open := func() (syscall.Handle, error) {
		return syscall.CreateFile(name, syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	}
	handle, err := open()
	if err != nil {
		t.Fatal(err)
	}
	var returned uint32
	err = syscall.DeviceIoControl(handle, setReparsePoint, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil)
	closeErr := syscall.CloseHandle(handle)
	if err != nil {
		t.Fatalf("set non-alias reparse tag on %q: %v", path, err)
	}
	t.Cleanup(func() {
		handle, err := open()
		if err != nil {
			t.Errorf("open reparse file for cleanup: %v", err)
			return
		}
		defer syscall.CloseHandle(handle)
		if err := syscall.DeviceIoControl(handle, deleteReparsePoint, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil); err != nil {
			t.Errorf("delete non-alias reparse tag: %v", err)
		}
	})
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	var data syscall.Win32finddata
	find, err := syscall.FindFirstFile(name, &data)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.FindClose(find); err != nil {
		t.Fatal(err)
	}
	if data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT == 0 || data.Reserved0 != tag {
		t.Fatalf("custom reparse setup did not persist: attributes=%#x tag=%#x", data.FileAttributes, data.Reserved0)
	}
}
