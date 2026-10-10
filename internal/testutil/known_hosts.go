package testutil

import (
	"os"
	"path/filepath"
	"strings"
)

// SSHNewKeysReceived is the line ssh -v logs once it has verified the server's exchange signature, sent
// its NEWKEYS, and accepted the server's.
const SSHNewKeysReceived = "debug1: SSH2_MSG_NEWKEYS received"

// KnownHostsFile returns the path of ssh's "-o UserKnownHostsFile=PATH" argument as ssh reads it:
// without surrounding quotes, with "%%" as a literal "%", in the platform's separators.
func KnownHostsFile(args []string) (string, bool) {
	for i, arg := range args {
		if i > 0 && args[i-1] == "-o" && strings.HasPrefix(arg, "UserKnownHostsFile=") {
			path := strings.ReplaceAll(strings.Trim(strings.TrimPrefix(arg, "UserKnownHostsFile="), `"`), "%%", "%")
			return filepath.FromSlash(path), true
		}
	}
	return "", false
}

// RecordKnownHost appends line to the file of ssh's "-o UserKnownHostsFile=PATH" argument, as ssh does
// under StrictHostKeyChecking=accept-new in its host key callback, which runs when the host key arrives
// and before ssh verifies the server's exchange signature. The file must already exist.
func RecordKnownHost(args []string, line string) error {
	path, ok := KnownHostsFile(args)
	if !ok {
		return os.ErrNotExist
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(line)
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	return writeErr
}
