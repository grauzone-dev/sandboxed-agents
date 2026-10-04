package cli_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

const (
	windowsSetReparsePoint      = 0x000900a4
	windowsDeleteReparsePoint   = 0x000900ac
	windowsPrivilegeNotAssigned = syscall.Errno(1300)
	windowsRelativeSymlinkFlag  = 1
)

func TestWindowsUpRejectsNestedRootRelativeProtectedWorkspaceLinkBeforePodman(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	root := workspaceFixture(t)
	protected := configureWindowsWorkspacePaths(t, root)["ssh"]
	workspace := filepath.Join(root, "project")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(workspace, "protected-link")
	target := strings.TrimPrefix(protected, filepath.VolumeName(protected))
	if err := setWindowsWorkspaceRawRelativeSymlink(t, protected, target, alias); err != nil {
		t.Fatal(err)
	}
	assertWindowsWorkspaceLinkTarget(t, alias, target, protected)
	assertWindowsProtectedWorkspace(t, fakes, workspace, protected)
}

func TestWindowsUpBindsRootRelativeWorkspaceLinkFromItsCanonicalTarget(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	root := workspaceFixture(t)
	configureWindowsWorkspacePaths(t, root)
	workspace := filepath.Join(root, "project")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "workspace-link")
	target := strings.TrimPrefix(workspace, filepath.VolumeName(workspace))
	if err := setWindowsWorkspaceRawRelativeSymlink(t, workspace, target, alias); err != nil {
		t.Fatal(err)
	}
	assertWindowsWorkspaceLinkTarget(t, alias, target, workspace)
	responses := append(healthyWindowsPodman(), upObjectResponses(nil, false, nil, nil)[1:]...)
	responses = append(responses, make([]testutil.Response, 5)...)
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "up", "agent01", alias)
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

func TestWindowsUpRejectsDriveRelativeWorkspaceLinksBeforePodman(t *testing.T) {
	for _, location := range []string{"workspace", "nested"} {
		t.Run(location, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			root := workspaceFixture(t)
			configureWindowsWorkspacePaths(t, root)
			workspace := filepath.Join(root, "project")
			if err := os.Mkdir(workspace, 0700); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(root, "workspace-link")
			if location == "nested" {
				alias = filepath.Join(workspace, "nested-link")
			}
			target := filepath.VolumeName(workspace) + "project"
			if err := setWindowsWorkspaceRawRelativeSymlink(t, workspace, target, alias); err != nil {
				t.Fatal(err)
			}
			got, err := os.Readlink(alias)
			if err != nil || got != target {
				t.Fatalf("raw drive-relative target=%q error=%v want=%q", got, err, target)
			}
			if location == "workspace" {
				workspace = alias
			}
			stdout, stderr, status := runCLI(t, "windows", "up", "agent01", workspace)
			if status == 0 || stdout != "" || !strings.Contains(stderr, fmt.Sprintf("%q", workspace)) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("drive-relative workspace link called Podman")
			}
			assertNoSSH(t, fakes)
		})
	}
}

func setWindowsWorkspaceRawRelativeSymlink(t *testing.T, initialTarget, target, alias string) error {
	t.Helper()
	if err := os.Symlink(initialTarget, alias); err != nil {
		return err
	}
	encoded, err := syscall.UTF16FromString(target)
	if err != nil {
		return err
	}
	encoded = encoded[:len(encoded)-1]
	buffer := make([]byte, 20+len(encoded)*2)
	binary.LittleEndian.PutUint32(buffer[:4], syscall.IO_REPARSE_TAG_SYMLINK)
	binary.LittleEndian.PutUint16(buffer[4:6], uint16(len(buffer)-8))
	binary.LittleEndian.PutUint16(buffer[10:12], uint16(len(encoded)*2))
	binary.LittleEndian.PutUint16(buffer[14:16], uint16(len(encoded)*2))
	binary.LittleEndian.PutUint32(buffer[16:20], windowsRelativeSymlinkFlag)
	for index, unit := range encoded {
		binary.LittleEndian.PutUint16(buffer[20+index*2:], unit)
	}
	handle, err := openWindowsWorkspaceReparsePoint(alias)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)
	var returned uint32
	write := func() error {
		return syscall.DeviceIoControl(handle, windowsSetReparsePoint, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil)
	}
	if err := write(); errors.Is(err, syscall.ERROR_PRIVILEGE_NOT_HELD) {
		restore, err := enableWindowsWorkspaceSymlinkPrivilege()
		if err != nil {
			return err
		}
		defer restore()
		return write()
	} else {
		return err
	}
}

func enableWindowsWorkspaceSymlinkPrivilege() (func(), error) {
	process, err := syscall.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	var token syscall.Token
	if err := syscall.OpenProcessToken(process, syscall.TOKEN_ADJUST_PRIVILEGES|syscall.TOKEN_QUERY, &token); err != nil {
		return nil, err
	}
	closeToken := func() { token.Close() }
	name, err := syscall.UTF16PtrFromString("SeCreateSymbolicLinkPrivilege")
	if err != nil {
		closeToken()
		return nil, err
	}
	api := syscall.NewLazyDLL("advapi32.dll")
	lookup := api.NewProc("LookupPrivilegeValueW")
	adjust := api.NewProc("AdjustTokenPrivileges")
	var privileges [16]byte
	binary.LittleEndian.PutUint32(privileges[:4], 1)
	binary.LittleEndian.PutUint32(privileges[12:], 2)
	result, _, callErr := lookup.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&privileges[4])))
	if result == 0 {
		closeToken()
		return nil, callErr
	}
	var previous [16]byte
	var length uint32
	result, _, callErr = adjust.Call(uintptr(token), 0, uintptr(unsafe.Pointer(&privileges[0])), uintptr(len(previous)), uintptr(unsafe.Pointer(&previous[0])), uintptr(unsafe.Pointer(&length)))
	if result == 0 || errors.Is(callErr, windowsPrivilegeNotAssigned) {
		closeToken()
		return nil, fmt.Errorf("enable symlink creation privilege: %v", callErr)
	}
	return func() {
		adjust.Call(uintptr(token), 0, uintptr(unsafe.Pointer(&previous[0])), 0, 0, 0)
		closeToken()
	}, nil
}

func assertWindowsWorkspaceLinkTarget(t *testing.T, alias, target, resolved string) {
	t.Helper()
	got, err := os.Readlink(alias)
	if err != nil || got != target {
		t.Fatalf("raw link target=%q error=%v want=%q", got, err, target)
	}
	linkInfo, err := os.Stat(alias)
	if err != nil {
		t.Fatalf("raw link does not resolve on Windows: %v", err)
	}
	targetInfo, err := os.Stat(resolved)
	if err != nil || !os.SameFile(linkInfo, targetInfo) {
		t.Fatalf("raw link does not reach intended Windows target %q: error=%v", resolved, err)
	}
}

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
	var buffer [24]byte
	binary.LittleEndian.PutUint32(buffer[:4], tag)
	copy(buffer[8:], []byte{0xe1, 0x8e, 0x5e, 0x1c, 0xb7, 0xe2, 0xf8, 0x43, 0x94, 0xb5, 0x27, 0xc9, 0xc3, 0x88, 0x49, 0x95})
	handle, err := openWindowsWorkspaceReparsePoint(path)
	if err != nil {
		t.Fatal(err)
	}
	var returned uint32
	err = syscall.DeviceIoControl(handle, windowsSetReparsePoint, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil)
	closeErr := syscall.CloseHandle(handle)
	if err != nil {
		t.Fatalf("set non-alias reparse tag on %q: %v", path, err)
	}
	t.Cleanup(func() {
		handle, err := openWindowsWorkspaceReparsePoint(path)
		if err != nil {
			t.Errorf("open reparse file for cleanup: %v", err)
			return
		}
		defer syscall.CloseHandle(handle)
		if err := syscall.DeviceIoControl(handle, windowsDeleteReparsePoint, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil); err != nil {
			t.Errorf("delete non-alias reparse tag: %v", err)
		}
	})
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
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

func openWindowsWorkspaceReparsePoint(path string) (syscall.Handle, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	return syscall.CreateFile(name, syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
}
