//go:build !windows

package platform

import "runtime"

func CurrentHost() Host {
	return Host{OS: runtime.GOOS, Architecture: runtime.GOARCH}
}
