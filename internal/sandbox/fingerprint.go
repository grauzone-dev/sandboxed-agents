package sandbox

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type Fingerprint struct {
	*sandboxObjects
}

func NewFingerprint(name, group string, run process.Runner) *Fingerprint {
	return &Fingerprint{sandboxObjects: newSandboxObjects(name, group, run, process.Streams{})}
}

func (fingerprint *Fingerprint) Print(ctx context.Context, output io.Writer) error {
	var result bytes.Buffer
	for _, key := range []struct{ fileType, algorithm string }{
		{"ed25519", "ssh-ed25519"},
		{"ecdsa", "ecdsa-sha2-nistp256"},
		{"rsa", "ssh-rsa"},
	} {
		path := "/etc/ssh/ssh_host_" + key.fileType + "_key.pub"
		var publicKey, diagnostic bytes.Buffer
		status, err := fingerprint.run(ctx, process.Request{
			Name: "podman", Args: []string{"exec", "--user=0:0", fingerprint.container, "cat", path},
			Streams: process.Streams{Stdout: &publicKey, Stderr: &diagnostic},
		})
		if err != nil {
			return fmt.Errorf(fingerprintReadFailedFormat, path, err)
		}
		if status != 0 {
			return fmt.Errorf(fingerprintPodmanFailedFormat, path, status, strings.TrimSpace(diagnostic.String()))
		}
		blob, ok := hostPublicKey(publicKey.String(), key.algorithm)
		if !ok {
			return fmt.Errorf(fingerprintInvalidKeyFormat, path)
		}
		digest := sha256.Sum256(blob)
		fmt.Fprintf(&result, "%s SHA256:%s\n", key.algorithm, base64.RawStdEncoding.EncodeToString(digest[:]))
	}
	_, err := output.Write(result.Bytes())
	return err
}

func hostPublicKey(text, algorithm string) ([]byte, bool) {
	text = strings.TrimSpace(text)
	if strings.ContainsAny(text, "\r\n") {
		return nil, false
	}
	fields := strings.Fields(text)
	if len(fields) < 2 || fields[0] != algorithm {
		return nil, false
	}
	blob, err := base64.StdEncoding.Strict().DecodeString(fields[1])
	if err != nil {
		return nil, false
	}
	rest := blob
	var parts [][]byte
	for len(rest) > 0 {
		if len(rest) < 4 {
			return nil, false
		}
		length := binary.BigEndian.Uint32(rest[:4])
		rest = rest[4:]
		if uint64(length) > uint64(len(rest)) {
			return nil, false
		}
		parts = append(parts, rest[:length])
		rest = rest[length:]
	}
	if len(parts) < 2 || string(parts[0]) != algorithm {
		return nil, false
	}
	switch algorithm {
	case "ssh-ed25519":
		return blob, len(parts) == 2 && len(parts[1]) == 32
	case "ecdsa-sha2-nistp256":
		if len(parts) != 3 || string(parts[1]) != "nistp256" {
			return nil, false
		}
		_, err := ecdh.P256().NewPublicKey(parts[2])
		return blob, err == nil
	case "ssh-rsa":
		return blob, len(parts) == 3 && len(parts[1]) > 0 && len(parts[2]) > 0
	}
	return nil, false
}
