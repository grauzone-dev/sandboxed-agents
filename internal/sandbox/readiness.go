package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sshkeys"
)

func WaitReady(ctx context.Context, container string, port int, run process.Runner) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	managerReady, sshReady := false, false
	var managerErr, sshErr error
	for {
		if ctx.Err() != nil {
			return fmt.Errorf(readinessWaitFailureFormat, container, errors.Join(ctx.Err(), managerErr, sshErr))
		}
		if !managerReady {
			managerErr = readinessManagerProbe(ctx, container, run)
			managerReady = managerErr == nil
		}
		if !sshReady && ctx.Err() == nil {
			sshErr = readinessSSHProbe(ctx, port, run)
			sshReady = sshErr == nil
		}
		if managerReady && sshReady && ctx.Err() == nil {
			return nil
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func readinessManagerProbe(ctx context.Context, container string, run process.Runner) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return probeManagerVersion(ctx, container, run)
}

func probeManagerVersion(ctx context.Context, container string, run process.Runner) error {
	var output bytes.Buffer
	status, err := run(ctx, process.Request{Name: "podman", Args: managerArgs(container, "version"), Streams: process.Streams{Stdout: &output, Stderr: io.Discard}})
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if status != 0 || !managerVersionResponse.Match(output.Bytes()) || strings.ContainsFunc(strings.TrimSuffix(output.String(), "\n"), unicode.IsControl) {
		return errors.New(readinessManagerError)
	}
	return nil
}

// readinessSSHProbe runs the host's ssh, not ssh-keyscan: the Windows OpenSSH 9.5 ssh-keyscan
// proposes sntrup761x25519 although its build cannot compute it (Win32-OpenSSH #2140), while ssh
// proposes only supported algorithms. Every authentication method is off, so ssh exits non-zero and
// its exit status is not the signal. Under accept-new, ssh records the host key in a private
// known_hosts file when the host key arrives, which is before it verifies the server's exchange
// signature (kexgen.c input_kex_gen_reply). Only after that verification does ssh send NEWKEYS and
// accept the server's, which -v logs as "SSH2_MSG_NEWKEYS received" (kex.c); both are required.
func readinessSSHProbe(ctx context.Context, port int, run process.Runner) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "sandboxed-agents-readiness-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0600); err != nil {
		return err
	}
	diagnostics := &cappedBuffer{limit: 64 << 10}
	if _, err := run(ctx, process.Request{Name: "ssh", Args: readinessSSHArgs(port, knownHosts), Streams: process.Streams{Stdout: io.Discard, Stderr: diagnostics}}); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !slices.Contains(strings.Split(strings.ReplaceAll(diagnostics.String(), "\r", ""), "\n"), "debug1: SSH2_MSG_NEWKEYS received") {
		return errors.New(readinessSSHError)
	}
	recorded, err := os.ReadFile(knownHosts)
	if err != nil {
		return err
	}
	host := "127.0.0.1"
	if port != 22 {
		host = "[127.0.0.1]:" + strconv.Itoa(port)
	}
	for _, line := range strings.Split(string(recorded), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != host {
			continue
		}
		key := strings.TrimSpace(line)[len(host):]
		if _, err := sshkeys.ParsePublicKey([]byte(key)); err == nil {
			return nil
		}
	}
	return errors.New(readinessSSHError)
}

func readinessSSHArgs(port int, knownHosts string) []string {
	return []string{"-F", "none", "-v", "-T", "-n",
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=1", "-o", "ConnectionAttempts=1",
		"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + sshConfigPath(strings.ReplaceAll(knownHosts, "%", "%%")),
		"-o", "GlobalKnownHostsFile=none", "-o", "HashKnownHosts=no", "-o", "UpdateHostKeys=no", "-o", "CheckHostIP=no",
		"-o", "HostKeyAlgorithms=ssh-ed25519",
		"-o", "PubkeyAuthentication=no", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "-o", "IdentityAgent=none",
		"-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no",
		"-p", strconv.Itoa(port), "-l", "agent", "--", "127.0.0.1", "true"}
}

// cappedBuffer keeps the first limit bytes of ssh's diagnostics, which hold the key exchange lines.
type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
