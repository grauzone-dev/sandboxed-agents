package release

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ChecksumFilename  = "SHA256SUMS"
	LinuxExecutable   = "sandboxed-agents-linux-amd64"
	WindowsExecutable = "sandboxed-agents-windows-amd64.exe"
)

func Checksums(directory string) ([]byte, error) {
	var contents strings.Builder
	for _, name := range []string{LinuxExecutable, WindowsExecutable} {
		binary, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&contents, "%x  %s\n", sha256.Sum256(binary), name)
	}
	return []byte(contents.String()), nil
}
