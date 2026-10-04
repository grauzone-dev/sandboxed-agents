package sandbox

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

func (up *Up) TranslateWorkspace(automountRoot string) error {
	if up.workspaceSource == "" || up.workspaceOS != "windows" {
		return nil
	}
	if path.Clean(automountRoot) != "/mnt" {
		return fmt.Errorf(workspaceAutomountError, automountRoot)
	}
	source, err := windowsBindSource(up.workspaceHost)
	if err != nil {
		return err
	}
	up.workspaceSource = source
	return nil
}

func windowsBindSource(hostPath string) (string, error) {
	source := strings.ReplaceAll(hostPath, `\`, "/")
	source = strings.TrimPrefix(source, "//?/")
	if len(source) < 3 || source[1:3] != ":/" || !windowsDriveLetter(source[0]) {
		return "", fmt.Errorf(workspaceWindowsPathError, hostPath)
	}
	return "/mnt/" + strings.ToLower(source[:1]) + source[2:], nil
}

func windowsWorkspacePath(source string) (string, bool) {
	if !strings.HasPrefix(source, "/mnt/") || len(source) < 6 || !windowsDriveLetter(source[5]) || len(source) > 6 && source[6] != '/' {
		return "", false
	}
	suffix := "/"
	if len(source) > 6 {
		suffix = source[6:]
	}
	return filepath.FromSlash(strings.ToUpper(source[5:6]) + ":" + suffix), true
}

func windowsDriveLetter(letter byte) bool {
	return letter >= 'a' && letter <= 'z' || letter >= 'A' && letter <= 'Z'
}
