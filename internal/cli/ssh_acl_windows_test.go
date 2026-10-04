package cli_test

import (
	"encoding/binary"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestWindowsSSHInstallRestrictsKeysAndCreatedUserConfiguration(t *testing.T) {
	fakes, fixture, sshDir, state := sshSetupHost(t, true)
	scriptSSHInstall(t, fakes, true, "agent01", "default", true, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{Stdout: sshHostPublicKey + "\n"}, testutil.Response{WantStdin: sshClientPublicKey + "\n"})
	scriptSSHDefaults(fakes, "agent01")
	stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01", "--install")
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	assertWindowsSSHSetupACLs(t, state, "agent01")
	assertWindowsSSHACL(t, filepath.Join(sshDir, "config"), false)
	assertWindowsSSHACL(t, sshDir, true)
	assertWindowsSSHACL(t, state, true)
	assertWindowsSSHACL(t, filepath.Join(state, "group-default"), true)
}

func TestWindowsSSHInstallPreservesExistingUserConfigurationPermissions(t *testing.T) {
	for _, protected := range []bool{false, true} {
		name := "inherited"
		if protected {
			name = "protected"
		}
		t.Run(name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, true)
			if err := os.Mkdir(sshDir, 0700); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(sshDir, "config")
			personal := "Host personal\n  HostName personal.example\n"
			if err := os.WriteFile(configPath, []byte(personal), 0600); err != nil {
				t.Fatal(err)
			}
			account, err := user.Current()
			if err != nil {
				t.Fatal(err)
			}
			protection := ""
			if protected {
				protection = "P"
			}
			setWindowsSSHFixtureDACL(t, configPath, "D:"+protection+"(A;;FA;;;"+account.Uid+")(A;;FR;;;BU)", protected)
			beforeConfig := windowsSSHDescriptor(t, configPath)
			beforeDirectory := windowsSSHDescriptor(t, sshDir)
			for _, sandboxName := range []string{"agent01", "agent02"} {
				scriptSSHInstall(t, fakes, true, sandboxName, "default", true, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{Stdout: sshHostPublicKey + "\n"}, testutil.Response{WantStdin: sshClientPublicKey + "\n"})
				scriptSSHDefaults(fakes, sandboxName)
				stdout, stderr, status := runCLI(t, fixture, "ssh-config", sandboxName, "--install")
				if status != 0 || stderr != "" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if after := windowsSSHDescriptor(t, configPath); !reflect.DeepEqual(after, beforeConfig) {
					t.Fatalf("user config security changed: before=%+v after=%+v", beforeConfig, after)
				}
				if after := windowsSSHDescriptor(t, sshDir); !reflect.DeepEqual(after, beforeDirectory) {
					t.Fatalf("existing SSH directory security changed: before=%+v after=%+v", beforeDirectory, after)
				}
				assertWindowsSSHSetupACLs(t, state, sandboxName)
			}
			contents, err := os.ReadFile(configPath)
			if err != nil || !strings.HasSuffix(string(contents), personal) || strings.Count(string(contents), "Include ") != 1 {
				t.Fatalf("user config=%q error=%v", contents, err)
			}
		})
	}
}

func setWindowsSSHFixtureDACL(t *testing.T, path, sddl string, protected bool) {
	t.Helper()
	text, err := syscall.UTF16PtrFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	advapi := syscall.NewLazyDLL("advapi32.dll")
	var descriptor unsafe.Pointer
	ok, _, callErr := advapi.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW").Call(uintptr(unsafe.Pointer(text)), 1, uintptr(unsafe.Pointer(&descriptor)), 0)
	if ok == 0 {
		t.Fatal(callErr)
	}
	defer syscall.LocalFree(syscall.Handle(descriptor))
	var acl unsafe.Pointer
	var present, defaulted int32
	ok, _, callErr = advapi.NewProc("GetSecurityDescriptorDacl").Call(uintptr(descriptor), uintptr(unsafe.Pointer(&present)), uintptr(unsafe.Pointer(&acl)), uintptr(unsafe.Pointer(&defaulted)))
	if ok == 0 {
		t.Fatal(callErr)
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	flags := uintptr(0x20000004)
	if protected {
		flags = 0x80000004
	}
	status, _, _ := advapi.NewProc("SetNamedSecurityInfoW").Call(uintptr(unsafe.Pointer(name)), 1, flags, 0, 0, uintptr(acl), 0)
	if status != 0 {
		t.Fatal(syscall.Errno(status))
	}
}

func assertWindowsSSHSetupACLs(t *testing.T, state, name string) {
	t.Helper()
	keyDirectory := filepath.Join(state, "group-default", "ssh", "sandbox-"+map[string]string{"agent01": "6167656e743031", "agent02": "6167656e743032"}[name])
	for _, file := range []string{"id_ed25519", "id_ed25519.pub", "known_hosts", "entry"} {
		assertWindowsSSHACL(t, filepath.Join(keyDirectory, file), false)
	}
	assertWindowsSSHACL(t, filepath.Join(state, "group-default", "ssh", "config"), false)
	for _, directory := range []string{filepath.Join(state, "group-default", "ssh"), keyDirectory} {
		assertWindowsSSHACL(t, directory, true)
	}
}

func assertSSHSetupPermissions(t *testing.T, windows bool, state, sshDir string) {
	t.Helper()
	if windows {
		assertWindowsSSHSetupACLs(t, state, "agent01")
		assertWindowsSSHACL(t, filepath.Join(sshDir, "config"), false)
		assertWindowsSSHACL(t, sshDir, true)
	}
}

func assertWindowsSSHACL(t *testing.T, path string, directory bool) {
	t.Helper()
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	flags := byte(0)
	if directory {
		flags = 3
	}
	want := windowsSSHSecurity{Owner: account.Uid, Protected: true, Entries: []windowsSSHACE{{Flags: flags, Mask: 0x001f01ff, SID: account.Uid}}}
	if got := windowsSSHDescriptor(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("ACL %s = %+v, want %+v", path, got, want)
	}
}

type windowsSSHACE struct {
	Type, Flags byte
	Mask        uint32
	SID         string
}

type windowsSSHSecurity struct {
	Owner     string
	Protected bool
	Entries   []windowsSSHACE
}

func windowsSSHDescriptor(t *testing.T, path string) windowsSSHSecurity {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	advapi := syscall.NewLazyDLL("advapi32.dll")
	get := advapi.NewProc("GetFileSecurityW")
	var size uint32
	get.Call(uintptr(unsafe.Pointer(name)), 5, 0, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		t.Fatalf("no security descriptor size for %s", path)
	}
	buffer := make([]byte, size)
	ok, _, callErr := get.Call(uintptr(unsafe.Pointer(name)), 5, uintptr(unsafe.Pointer(&buffer[0])), uintptr(size), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		t.Fatal(callErr)
	}
	var owner *syscall.SID
	var defaulted int32
	ok, _, callErr = advapi.NewProc("GetSecurityDescriptorOwner").Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&owner)), uintptr(unsafe.Pointer(&defaulted)))
	if ok == 0 {
		t.Fatal(callErr)
	}
	ownerSID, err := owner.String()
	if err != nil {
		t.Fatal(err)
	}
	var control uint16
	var revision uint32
	ok, _, callErr = advapi.NewProc("GetSecurityDescriptorControl").Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&control)), uintptr(unsafe.Pointer(&revision)))
	if ok == 0 {
		t.Fatal(callErr)
	}
	security := windowsSSHSecurity{Owner: ownerSID, Protected: control&0x1000 != 0}
	var acl unsafe.Pointer
	var present int32
	ok, _, callErr = advapi.NewProc("GetSecurityDescriptorDacl").Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&present)), uintptr(unsafe.Pointer(&acl)), uintptr(unsafe.Pointer(&defaulted)))
	if ok == 0 || present == 0 || acl == nil {
		t.Fatalf("missing DACL for %s: %v", path, callErr)
	}
	header := unsafe.Slice((*byte)(acl), 8)
	count := binary.LittleEndian.Uint16(header[4:6])
	for index := uint16(0); index < count; index++ {
		var ace unsafe.Pointer
		ok, _, callErr = advapi.NewProc("GetAce").Call(uintptr(acl), uintptr(index), uintptr(unsafe.Pointer(&ace)))
		if ok == 0 {
			t.Fatal(callErr)
		}
		prefix := unsafe.Slice((*byte)(ace), 8)
		if prefix[0] != 0 && prefix[0] != 1 {
			t.Fatalf("unexpected ACE type %d for %s", prefix[0], path)
		}
		sid := (*syscall.SID)(unsafe.Add(ace, 8))
		trustee, err := sid.String()
		if err != nil {
			t.Fatal(err)
		}
		security.Entries = append(security.Entries, windowsSSHACE{Type: prefix[0], Flags: prefix[1], Mask: binary.LittleEndian.Uint32(prefix[4:8]), SID: trustee})
	}
	return security
}
