package integrations

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func ReadToken(streams process.Streams) (string, error) {
	if streams.Stdin == nil {
		return "", errors.New(AzdoTokenRequired)
	}
	var token string
	var err error
	if platform.IsTerminal(streams.Stdin) {
		if _, err = io.WriteString(streams.Stderr, AzdoTokenPrompt); err != nil {
			return "", errors.New(AzdoTokenReadFailure)
		}
		token, err = platform.ReadPassword(streams.Stdin.(*os.File))
		if _, newlineErr := io.WriteString(streams.Stderr, "\n"); newlineErr != nil && err == nil {
			err = newlineErr
		}
	} else {
		token, err = bufio.NewReader(io.LimitReader(streams.Stdin, 65537)).ReadString('\n')
		if err == io.EOF {
			err = nil
		}
		token = strings.TrimSuffix(strings.TrimSuffix(token, "\n"), "\r")
	}
	if err != nil {
		return "", errors.New(AzdoTokenReadFailure)
	}
	if token == "" {
		return "", errors.New(AzdoTokenRequired)
	}
	if len(token) > 65536 || strings.ContainsRune(token, 0) || strings.ContainsFunc(token, unicode.IsSpace) {
		return "", errors.New(AzdoInvalidToken)
	}
	return token, nil
}
