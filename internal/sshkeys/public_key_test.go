package sshkeys_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/sshkeys"
)

const publicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINdamAGCsQq31Uv+08lkBzoO4XLz2qYjJa8CGmj3B1Ea"

func TestPublicKeyCanonicalizesOneEd25519SSHKey(t *testing.T) {
	for _, input := range []string{publicKey, publicKey + " sandbox key\n", " \t" + publicKey + "\r\n", strings.Replace(publicKey, " ", "\t", 1)} {
		key, err := sshkeys.ParsePublicKey([]byte(input))
		if err != nil || key != publicKey {
			t.Fatalf("key=%q error=%v", key, err)
		}
	}
}

func TestPublicKeyRequiresExactlyOneCompleteEd25519WireEncoding(t *testing.T) {
	blob, err := base64.StdEncoding.DecodeString(strings.Fields(publicKey)[1])
	if err != nil {
		t.Fatal(err)
	}
	wrongType := append([]byte(nil), blob...)
	wrongType[4] = 'x'
	wrongTypeLength := append([]byte(nil), blob...)
	wrongTypeLength[3] = 10
	wrongKeyLength := append([]byte(nil), blob...)
	wrongKeyLength[18] = 31
	for _, input := range []string{
		"", "ssh-ed25519", "ssh-ed25519 !!!", "ssh-ed25519 YWJj", "ssh-rsa " + strings.Fields(publicKey)[1],
		"command=\"x\" " + publicKey, publicKey + "\n" + publicKey, publicKey + "\ncomment",
		"ssh-ed25519 " + base64.StdEncoding.EncodeToString(wrongType),
		"ssh-ed25519 " + base64.StdEncoding.EncodeToString(wrongTypeLength),
		"ssh-ed25519 " + base64.StdEncoding.EncodeToString(wrongKeyLength),
		"ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob[:50]),
		"ssh-ed25519 " + base64.StdEncoding.EncodeToString(append(blob, 0)),
		publicKey + strings.Repeat("x", 16*1024),
	} {
		if key, err := sshkeys.ParsePublicKey([]byte(input)); err == nil || key != "" {
			t.Fatalf("input=%q key=%q error=%v", input, key, err)
		}
	}
}

func TestPublicKeyRejectsControlCharacters(t *testing.T) {
	for _, control := range []string{"\x00", "\x01", "\x7f"} {
		if key, err := sshkeys.ParsePublicKey([]byte(publicKey + " comment" + control)); err == nil || key != "" {
			t.Fatalf("key=%q error=%v", key, err)
		}
	}
}
