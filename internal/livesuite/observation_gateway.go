package livesuite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func observeGateway(ctx context.Context, config Config, run process.Runner, group, name, image, container, gateway string) (result error) {
	address := net.ParseIP(gateway)
	if address == nil || address.To4() == nil || address.IsUnspecified() || address.IsLoopback() {
		return errors.New("gateway-address")
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("gateway-nonce: %w", err)
	}
	token := hex.EncodeToString(nonce)
	hostName, controlName := container+"-gateway-host", container+"-gateway-control"
	var created []string
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		for i := len(created) - 1; i >= 0; i-- {
			probeName := created[i]
			status, err := run(cleanup, process.Request{Name: "podman", Args: []string{"container", "exists", probeName}, Streams: process.Streams{Stderr: config.Stderr}})
			if err != nil {
				result = errors.Join(result, fmt.Errorf("gateway-cleanup-exists: %w", err))
				continue
			}
			if status > 1 {
				result = errors.Join(result, fmt.Errorf("gateway-cleanup-exists-%d", status))
				continue
			}
			if status == 1 {
				continue
			}
			data, err := readObservation(cleanup, run, "podman", []string{"container", "inspect", probeName}, config.Stderr)
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
			var records []struct {
				Name   string
				Config struct{ Labels map[string]string }
			}
			if json.Unmarshal(data, &records) != nil || len(records) != 1 || records[0].Name != probeName || records[0].Config.Labels[sandbox.OwnerLabel] != group || records[0].Config.Labels[sandbox.NameLabel] != name {
				result = errors.Join(result, errors.New("gateway-probe-owner-conflict"))
				continue
			}
			if _, err = readObservation(cleanup, run, "podman", []string{"stop", "--time", "2", probeName}, config.Stderr); err != nil {
				result = errors.Join(result, err)
				continue
			}
			_, err = readObservation(cleanup, run, "podman", []string{"rm", probeName}, config.Stderr)
			result = errors.Join(result, err)
		}
	}()
	create := func(probeName, network, script string, arguments ...string) error {
		args := []string{"run", "--detach", "--pull=never", "--name", probeName, "--label", sandbox.OwnerLabel + "=" + group, "--label", sandbox.NameLabel + "=" + name, "--network=" + network, "--user=1000:1000", "--cap-drop=all", "--security-opt=no-new-privileges", "--memory=128m", "--pids-limit=32", "--entrypoint=node", image, "-e", script}
		args = append(args, arguments...)
		created = append(created, probeName)
		_, err := readObservation(ctx, run, "podman", args, config.Stderr)
		return err
	}
	if err := create(hostName, "host", gatewayListenerScript, token); err != nil {
		return err
	}
	var port string
	readyCtx, readyCancel := context.WithTimeout(ctx, 10*time.Second)
	defer readyCancel()
	for {
		data, err := readObservation(readyCtx, run, "podman", []string{"exec", "--user=1000:1000", hostName, "node", "-e", `process.stdout.write(require('node:fs').readFileSync('/tmp/live-gateway-port','utf8'))`}, config.Stderr)
		if err == nil {
			value, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr != nil || value < 1 || value > 65535 {
				return errors.New("gateway-listener-port")
			}
			port = strconv.Itoa(value)
			break
		}
		select {
		case <-readyCtx.Done():
			return fmt.Errorf("gateway-listener-ready: %w", readyCtx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := create(controlName, "pasta:--map-gw", `setInterval(()=>{},1000);setTimeout(()=>process.exit(1),120000)`); err != nil {
		return err
	}
	probe := func(target, mode string) error {
		data, err := readObservation(ctx, run, "podman", []string{"exec", "--user=1000:1000", target, "node", "-e", gatewayClientScript, gateway, port, token, mode}, config.Stderr)
		if err != nil {
			return err
		}
		if string(data) != "pass" {
			return errors.New("gateway-observation")
		}
		return nil
	}
	if err := probe(controlName, "allowed"); err != nil {
		return fmt.Errorf("gateway-positive-control-before: %w", err)
	}
	if err := probe(container, "denied"); err != nil {
		return fmt.Errorf("gateway-denied: %w", err)
	}
	if err := probe(controlName, "allowed"); err != nil {
		return fmt.Errorf("gateway-positive-control-after: %w", err)
	}
	return nil
}
