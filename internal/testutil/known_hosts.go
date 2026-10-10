package testutil

import (
	"os"
	"path/filepath"
	"strings"
)

// SSHNewKeysReceived is the line ssh -v logs once it has verified the server's exchange signature, sent
// its NEWKEYS, and accepted the server's.
const SSHNewKeysReceived = "debug1: SSH2_MSG_NEWKEYS received"

// KnownHostsFile returns the path of ssh's "-o UserKnownHostsFile=PATH" argument as ssh reads it: a
// value in double quotes loses them, with \" and \\ inside them as a literal " and \; "%%" is a
// literal "%"; the result uses the platform's separators, and a relative path is joined to dir, the
// working directory of ssh ("" keeps it relative to the current one).
func KnownHostsFile(dir string, args []string) (string, bool) {
	for i, arg := range args {
		if i > 0 && args[i-1] == "-o" && strings.HasPrefix(arg, "UserKnownHostsFile=") {
			value := strings.TrimPrefix(arg, "UserKnownHostsFile=")
			if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
				value = strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(value[1 : len(value)-1])
			}
			path := filepath.FromSlash(strings.ReplaceAll(value, "%%", "%"))
			if dir != "" && !filepath.IsAbs(path) {
				path = filepath.Join(dir, path)
			}
			return path, true
		}
	}
	return "", false
}

// RecordKnownHost appends line to the file of ssh's "-o UserKnownHostsFile=PATH" argument, resolved as
// KnownHostsFile does, as ssh does under StrictHostKeyChecking=accept-new in its host key callback, which
// runs when the host key arrives and before ssh verifies the server's exchange signature. The file must
// already exist.
func RecordKnownHost(dir string, args []string, line string) error {
	path, ok := KnownHostsFile(dir, args)
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
