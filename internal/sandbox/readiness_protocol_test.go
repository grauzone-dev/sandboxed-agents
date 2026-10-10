package sandbox_test

import (
	"bufio"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

// These tests drive the host's real ssh client through sandbox.WaitReady against a minimal SSH server
// that performs a curve25519-sha256 key exchange with an Ed25519 host key. OpenSSH runs its host key
// callback, which records the key under StrictHostKeyChecking=accept-new, before it verifies the
// server's exchange signature (kexgen.c input_kex_gen_reply), so a recorded key alone does not show a
// completed key exchange.
func TestSandboxReadinessRequiresAVerifiedKeyExchangeFromTheRealSSHClient(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh is not on PATH")
	}
	for _, mode := range []struct {
		name  string
		ready bool
	}{{"valid-exchange", true}, {"corrupted-exchange-signature", false}, {"host-key-without-signature", false}} {
		t.Run(mode.name, func(t *testing.T) {
			observer := debug55OriginalObserver(t, mode.name, mode.ready) // DEBUG-55 temporary: nil unless DEBUG_55_ORIGINAL_OBSERVE is set
			port := startTestSSHServer(t, mode.name)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", port, func(ctx context.Context, request process.Request) (int, error) {
				if request.Name == "podman" {
					fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
					return 0, nil
				}
				return observer.run(ctx, request, platform.Run) // DEBUG-55 temporary: platform.Run unchanged when observer is nil
			})
			observer.result(err) // DEBUG-55 temporary
			if mode.ready && err != nil {
				t.Fatalf("a completed key exchange was not accepted: %v", err)
			}
			if !mode.ready && err == nil {
				t.Fatal("readiness accepted an SSH server that did not complete a verified key exchange")
			}
		})
	}
}

func startTestSSHServer(t *testing.T, mode string) int {
	t.Helper()
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				serveTestSSHKeyExchange(debug55ServerConn(t, conn), hostKey, mode) // DEBUG-55 temporary: conn unchanged unless observed
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

func serveTestSSHKeyExchange(conn net.Conn, hostKey ed25519.PrivateKey, mode string) {
	const serverVersion = "SSH-2.0-SandboxedAgentsReadinessTest"
	if _, err := io.WriteString(conn, serverVersion+"\r\n"); err != nil {
		return
	}
	reader := bufio.NewReader(conn)
	clientVersion, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	clientVersion = strings.TrimRight(clientVersion, "\r\n")
	serverInit := []byte{20}
	cookie := make([]byte, 16)
	rand.Read(cookie)
	serverInit = append(serverInit, cookie...)
	for _, list := range []string{"curve25519-sha256,curve25519-sha256@libssh.org", "ssh-ed25519", "aes128-ctr", "aes128-ctr", "hmac-sha2-256", "hmac-sha2-256", "none", "none", "", ""} {
		serverInit = sshString(serverInit, []byte(list))
	}
	serverInit = append(serverInit, 0, 0, 0, 0, 0)
	if writeSSHPacket(conn, serverInit) != nil {
		return
	}
	clientInit, err := readSSHPacket(reader)
	if err != nil || len(clientInit) == 0 || clientInit[0] != 20 {
		return
	}
	init, err := readSSHPacket(reader)
	if err != nil || len(init) < 5 || init[0] != 30 {
		return
	}
	clientPublic, ok := sshStringAt(init[1:])
	if !ok {
		return
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return
	}
	peer, err := ecdh.X25519().NewPublicKey(clientPublic)
	if err != nil {
		return
	}
	secret, err := ephemeral.ECDH(peer)
	if err != nil {
		return
	}
	hostBlob := sshString(sshString(nil, []byte("ssh-ed25519")), hostKey.Public().(ed25519.PublicKey))
	serverPublic := ephemeral.PublicKey().Bytes()
	var exchange []byte
	for _, part := range [][]byte{[]byte(clientVersion), []byte(serverVersion), clientInit, serverInit, hostBlob, clientPublic, serverPublic} {
		exchange = sshString(exchange, part)
	}
	exchange = sshMPInt(exchange, secret)
	hash := sha256.Sum256(exchange)
	signature := ed25519.Sign(hostKey, hash[:])
	if mode == "corrupted-exchange-signature" {
		signature[0] ^= 0xff
	}
	reply := sshString([]byte{31}, hostBlob)
	if mode != "host-key-without-signature" {
		reply = sshString(reply, serverPublic)
		reply = sshString(reply, sshString(sshString(nil, []byte("ssh-ed25519")), signature))
	}
	if writeSSHPacket(conn, reply) != nil || mode != "valid-exchange" {
		io.Copy(io.Discard, reader)
		return
	}
	if writeSSHPacket(conn, []byte{21}) != nil {
		return
	}
	readSSHPacket(reader)
}

func sshString(buffer, value []byte) []byte {
	buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(value)))
	return append(buffer, value...)
}

func sshStringAt(buffer []byte) ([]byte, bool) {
	if len(buffer) < 4 || uint32(len(buffer)-4) < binary.BigEndian.Uint32(buffer) {
		return nil, false
	}
	return buffer[4 : 4+binary.BigEndian.Uint32(buffer)], true
}

func sshMPInt(buffer, value []byte) []byte {
	for len(value) > 0 && value[0] == 0 {
		value = value[1:]
	}
	if len(value) > 0 && value[0]&0x80 != 0 {
		value = append([]byte{0}, value...)
	}
	return sshString(buffer, value)
}

func writeSSHPacket(w io.Writer, payload []byte) error {
	padding := 8 - (5+len(payload))%8
	if padding < 4 {
		padding += 8
	}
	packet := binary.BigEndian.AppendUint32(nil, uint32(1+len(payload)+padding))
	packet = append(packet, byte(padding))
	packet = append(packet, payload...)
	packet = append(packet, make([]byte, padding)...)
	_, err := w.Write(packet)
	return err
}

func readSSHPacket(r io.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header)
	if length < 5 || length > 35000 {
		return nil, fmt.Errorf("invalid packet length %d", length)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	padding := int(body[0])
	if padding >= len(body) {
		return nil, fmt.Errorf("invalid padding %d", padding)
	}
	return body[1 : len(body)-padding], nil
}
