package buildenv

import (
	"os"
	"strings"
)

// ForTarget returns the current environment with fixed target, cgo, and build flag
// settings for goos and goarch, and with module and toolchain downloads disabled.
func ForTarget(goos, goarch string) []string {
	env := make([]string, 0, len(os.Environ())+11)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch strings.ToUpper(key) {
		case "GOOS", "GOARCH", "CGO_ENABLED", "GOAMD64", "GOPROXY", "GOSUMDB", "GOFLAGS", "GOEXPERIMENT", "GOWORK", "GOTOOLCHAIN", "GO111MODULE":
		default:
			env = append(env, value)
		}
	}
	return append(env, "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOAMD64=v1", "GOPROXY=off", "GOSUMDB=off", "GOFLAGS=", "GOEXPERIMENT=", "GOWORK=off", "GOTOOLCHAIN=local", "GO111MODULE=on")
}
