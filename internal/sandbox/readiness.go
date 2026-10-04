package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
			return fmt.Errorf(readinessTimeoutFormat, container, errors.Join(ctx.Err(), managerErr, sshErr))
		}
		if !managerReady {
			managerErr = readinessManagerProbe(ctx, container, run)
			managerReady = managerErr == nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf(readinessTimeoutFormat, container, errors.Join(ctx.Err(), managerErr, sshErr))
		}
		if !sshReady {
			sshErr = readinessSSHProbe(ctx, port, run)
			sshReady = sshErr == nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf(readinessTimeoutFormat, container, errors.Join(ctx.Err(), managerErr, sshErr))
		}
		if managerReady && sshReady {
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

func readinessSSHProbe(ctx context.Context, port int, run process.Runner) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var output bytes.Buffer
	status, err := run(ctx, process.Request{Name: "ssh-keyscan", Args: []string{"-T", "1", "-t", "ed25519", "-p", strconv.Itoa(port), "127.0.0.1"}, Streams: process.Streams{Stdout: &output, Stderr: io.Discard}})
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if status != 0 {
		return fmt.Errorf(readinessSSHProcessFormat, status)
	}
	host := "127.0.0.1"
	if port != 22 {
		host = "[127.0.0.1]:" + strconv.Itoa(port)
	}
	for _, line := range strings.Split(output.String(), "\n") {
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
