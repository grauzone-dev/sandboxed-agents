package sandbox

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

const SSHPortLabel = "io.github.sandboxed-agents.ssh-port"

func recordedSSHPort(name string, labels map[string]string) (int, error) {
	port, err := parseSSHPort(labels[SSHPortLabel])
	if err != nil {
		return 0, fmt.Errorf("sandbox %s has no valid recorded SSH port; run sandboxed-agents remove %s followed by sandboxed-agents up %s", name, name, name)
	}
	return port, nil
}

func parseSSHPort(value string) (int, error) {
	if !integerLimitPattern.MatchString(value) {
		return 0, strconv.ErrSyntax
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, strconv.ErrRange
	}
	return port, nil
}

func sshPortAvailable(port int) (bool, error) {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		if sshBindUnavailable(err) {
			return false, nil
		}
		return false, fmt.Errorf("check SSH port %d on 127.0.0.1: %w", port, err)
	}
	if err := listener.Close(); err != nil {
		return false, fmt.Errorf("check SSH port %d on 127.0.0.1: %w", port, err)
	}
	return true, nil
}

func requireSSHPort(port int) error {
	free, err := sshPortAvailable(port)
	if err != nil {
		return err
	}
	if !free {
		return fmt.Errorf("SSH port %d on 127.0.0.1 is unavailable", port)
	}
	return nil
}

func reservedSSHPorts(ctx context.Context, run process.Runner) (map[int]bool, error) {
	containers, err := queryContainers(ctx, run)
	if err != nil {
		return nil, err
	}
	ports := make(map[int]bool)
	for _, container := range containers {
		name := container.Names[0]
		if !strings.HasPrefix(name, containerPrefix) && !strings.HasPrefix(name, backupPrefix) && (container.Labels[OwnerLabel] == "" || container.Labels[NameLabel] == "") {
			continue
		}
		value, recorded := container.Labels[SSHPortLabel]
		if !recorded {
			continue
		}
		port, err := parseSSHPort(value)
		if err != nil {
			return nil, fmt.Errorf("invalid recorded SSH port %q on container %s", value, name)
		}
		ports[port] = true
	}
	return ports, nil
}

func (up *Up) CheckSSHPort(ctx context.Context) error {
	if up.containerExists {
		port, err := recordedSSHPort(up.name, up.containerLabels)
		if err != nil {
			return err
		}
		if up.port != 0 && up.port != port {
			return fmt.Errorf("sandbox %s records SSH port %d; requested --port %d differs; run sandboxed-agents remove %s followed by sandboxed-agents up %s --port %d", up.name, port, up.port, up.name, up.name, up.port)
		}
		up.port = port
		if up.containerRunning {
			return nil
		}
		return requireSSHPort(port)
	}
	ports, err := reservedSSHPorts(ctx, up.run)
	if err != nil {
		return err
	}
	if up.port != 0 {
		if ports[up.port] {
			return fmt.Errorf("SSH port %d is already recorded on a sandbox container", up.port)
		}
		return requireSSHPort(up.port)
	}
	for port := 2222; port <= 65535; port++ {
		if ports[port] {
			continue
		}
		free, err := sshPortAvailable(port)
		if err != nil {
			return err
		}
		if free {
			up.port = port
			return nil
		}
	}
	return fmt.Errorf("no free SSH port on 127.0.0.1 from 2222 through 65535")
}
