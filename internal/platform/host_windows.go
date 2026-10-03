//go:build windows

package platform

import (
	"syscall"
	"unsafe"
)

type windowsVersion struct {
	size             uint32
	major            uint32
	minor            uint32
	build            uint32
	platform         uint32
	servicePack      [128]uint16
	servicePackMajor uint16
	servicePackMinor uint16
	suiteMask        uint16
	productType      byte
	reserved         byte
}

func CurrentHost() Host {
	host := Host{OS: "windows"}
	versionProc := syscall.NewLazyDLL("ntdll.dll").NewProc("RtlGetVersion")
	architectureProc := syscall.NewLazyDLL("kernel32.dll").NewProc("IsWow64Process2")
	if versionProc.Find() != nil || architectureProc.Find() != nil {
		return host
	}
	version := windowsVersion{}
	version.size = uint32(unsafe.Sizeof(version))
	status, _, _ := versionProc.Call(uintptr(unsafe.Pointer(&version)))
	if status != 0 {
		return host
	}
	currentProcess, err := syscall.GetCurrentProcess()
	if err != nil {
		return host
	}
	var processMachine, nativeMachine uint16
	ok, _, _ := architectureProc.Call(uintptr(currentProcess), uintptr(unsafe.Pointer(&processMachine)), uintptr(unsafe.Pointer(&nativeMachine)))
	if ok == 0 {
		return host
	}
	switch nativeMachine {
	case 0x014c:
		host.Architecture = "386"
	case 0x01c4:
		host.Architecture = "arm"
	case 0x8664:
		host.Architecture = "amd64"
	case 0xaa64:
		host.Architecture = "arm64"
	}
	host.WindowsMajor = version.major
	host.WindowsBuild = version.build
	host.WindowsWorkstation = version.productType == 1
	return host
}
