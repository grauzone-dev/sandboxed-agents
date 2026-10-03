package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

const MinimumWindowsPodman = "5.0.0"

type WindowsResult struct {
	Name    string
	Met     bool
	Message string
}

type Report struct {
	Results       []WindowsResult
	AutomountRoot string
}

func (report Report) Err() error {
	for _, result := range report.Results {
		if !result.Met {
			return errors.New(failedMessage)
		}
	}
	return nil
}

func (report *Report) add(name string, met bool, args ...any) {
	index := 0
	if met {
		index = 1
	}
	message := messages[name][index]
	if len(args) > 0 {
		message = fmt.Sprintf(message, args...)
	}
	report.Results = append(report.Results, WindowsResult{Name: name, Met: met, Message: message})
}

func CheckWindows(ctx context.Context, host platform.Host, run process.Runner) Report {
	var report Report
	if host.OS != "windows" {
		report.Results = []WindowsResult{{Name: "platform", Message: unsupportedMessage}}
		return report
	}
	report.add("windows", host.Architecture == "amd64" && host.WindowsMajor == 10 && host.WindowsBuild >= 22000 && host.WindowsWorkstation)
	output, clientOK := read(ctx, run, "--version")
	client := strings.TrimPrefix(strings.TrimSpace(string(output)), "podman version ")
	report.add("client", clientOK && versionAtLeast(client, MinimumWindowsPodman), MinimumWindowsPodman)
	var machines []struct {
		Name    string
		Default bool
		Running bool
		VMType  string
	}
	output, listOK := read(ctx, run, "machine", "list", "--format", "json")
	listOK = listOK && json.Unmarshal(output, &machines) == nil
	selected := -1
	if listOK {
		for index, machine := range machines {
			if machine.Default {
				selected = index
				break
			}
		}
		if selected < 0 && len(machines) == 1 {
			selected = 0
		}
	}
	var name, provider string
	var running, rootless bool
	if selected >= 0 {
		machine := machines[selected]
		name, provider = machine.Name, machine.VMType
		var inspected []struct {
			Name    string
			State   string
			Rootful *bool
		}
		output, ok := read(ctx, run, "machine", "inspect", name)
		if ok && json.Unmarshal(output, &inspected) == nil && len(inspected) == 1 && inspected[0].Name == name {
			running = machine.Running && inspected[0].State == "running"
			rootless = inspected[0].Rootful != nil && !*inspected[0].Rootful
		}
	}
	report.add("running_machine", running)
	report.add("wsl2", provider == "wsl")
	var serverOK, infoOK bool
	var server struct {
		Server struct{ Version string }
	}
	var info struct {
		Host struct {
			CgroupVersion     string
			CgroupControllers []string
			Security          struct{ Rootless *bool }
		}
	}
	if running {
		output, serverOK = read(ctx, run, "--connection", name, "version", "--format", "json")
		serverOK = serverOK && json.Unmarshal(output, &server) == nil
		output, infoOK = read(ctx, run, "--connection", name, "info", "--format", "json")
		infoOK = infoOK && json.Unmarshal(output, &info) == nil
	}
	report.add("machine_version", serverOK && versionAtLeast(server.Server.Version, MinimumWindowsPodman), MinimumWindowsPodman)
	report.add("rootless", rootless && infoOK && info.Host.Security.Rootless != nil && *info.Host.Security.Rootless)
	controllers := map[string]bool{}
	for _, controller := range info.Host.CgroupControllers {
		controllers[controller] = true
	}
	report.add("cgroups", infoOK && info.Host.CgroupVersion == "v2" && controllers["cpu"] && controllers["memory"] && controllers["pids"])
	_, sshErr := exec.LookPath("ssh")
	report.add("ssh", sshErr == nil)
	_, keygenErr := exec.LookPath("ssh-keygen")
	report.add("ssh_keygen", keygenErr == nil)
	if running && provider == "wsl" {
		output, ok := read(ctx, run, "machine", "ssh", name, "sh", "-c", "'if [ -e /etc/wsl.conf ] || [ -L /etc/wsl.conf ]; then cat /etc/wsl.conf; fi'")
		if ok {
			report.AutomountRoot, ok = automountRoot(string(output))
		}
		report.add("automount", ok)
	}
	return report
}

func read(ctx context.Context, run process.Runner, args ...string) ([]byte, bool) {
	var stdout, stderr bytes.Buffer
	status, err := run(ctx, process.Request{Name: "podman", Args: args, Streams: process.Streams{Stdout: &stdout, Stderr: &stderr}})
	return stdout.Bytes(), err == nil && status == 0
}

func versionAtLeast(version, minimum string) bool {
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	expected := strings.Split(minimum, ".")
	if len(parts) != 3 || strings.ContainsAny(version, "-+") {
		return false
	}
	var values [3]int
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return false
		}
		values[index] = value
	}
	for index, value := range values {
		threshold, _ := strconv.Atoi(expected[index])
		if value != threshold {
			return value > threshold
		}
	}
	return true
}

func automountRoot(config string) (string, bool) {
	root := "/mnt/"
	enabled := true
	section := ""
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return "", false
			}
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		if section != "automount" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return "", false
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		switch key {
		case "root":
			root = value
		case "enabled":
			switch strings.ToLower(value) {
			case "true":
				enabled = true
			case "false":
				enabled = false
			default:
				return "", false
			}
		}
	}
	if !enabled || !strings.HasPrefix(root, "/") || strings.ContainsAny(root, "\x00\r\n\\") {
		return "", false
	}
	return strings.TrimRight(path.Clean(root), "/") + "/", true
}
