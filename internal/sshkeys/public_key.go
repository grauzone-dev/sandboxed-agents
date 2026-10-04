package sshkeys

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
)

const MaxPublicKeyBytes = 16 * 1024

func ParsePublicKey(input []byte) (string, error) {
	invalid := errors.New("SSH public key is not a single ssh-ed25519 key line")
	if len(input) > MaxPublicKeyBytes {
		return "", invalid
	}
	for _, b := range input {
		if (b < 32 && b != '\t' && b != '\r' && b != '\n') || b == 127 {
			return "", invalid
		}
	}
	line := bytes.TrimSpace(input)
	if bytes.ContainsAny(line, "\r\n") {
		return "", invalid
	}
	fields := strings.Fields(string(line))
	if len(fields) < 2 || fields[0] != "ssh-ed25519" {
		return "", invalid
	}
	blob, err := base64.StdEncoding.Strict().DecodeString(fields[1])
	if err != nil || len(blob) != 51 {
		return "", invalid
	}
	if binary.BigEndian.Uint32(blob[:4]) != 11 || string(blob[4:15]) != "ssh-ed25519" || binary.BigEndian.Uint32(blob[15:19]) != 32 {
		return "", invalid
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob), nil
}
