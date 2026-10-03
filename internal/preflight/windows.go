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

const minimumWindowsPodman = "5.0.0"

type Status string

const (
	Met     Status = "ok"
	Missing Status = "missing"
	Unknown Status = "unknown"
)

type WindowsResult struct {
	Name     string
	Status   Status
	Required bool
	Message  string
}

type Report struct {
	Results       []WindowsResult
	AutomountRoot string
	err           error
}

func (report Report) Err() error {
	if report.err != nil {
		return report.err
	}
	for _, result := range report.Results {
		if result.Required && result.Status != Met {
			return errors.New(failedMessage)
		}
	}
	return nil
}

func (report *Report) add(name string, known, met bool, args ...any) {
	text, found := messages[name]
	if !found {
		panic(name)
	}
	status, message := Missing, text.Missing
	if !known {
		status, message = Unknown, text.Unknown
	} else if met {
		status, message = Met, text.Met
	}
	if status != Unknown && len(args) > 0 {
		message = fmt.Sprintf(message, args...)
	}
	report.Results = append(report.Results, WindowsResult{Name: name, Status: status, Required: !text.Auxiliary, Message: message})
}

func CheckWindows(ctx context.Context, host platform.Host, run process.Runner) Report {
	var report Report
	if host.OS != "windows" {
		report.Results = []WindowsResult{{Name: "platform", Status: Unknown, Required: true, Message: unsupportedMessage}}
		report.err = errors.New(unsupportedMessage)
		return report
	}
	report.add("windows", true, host.Architecture == "amd64" && host.WindowsMajor == 10 && host.WindowsBuild >= 22000 && host.WindowsWorkstation)
	_, clientPathErr := exec.LookPath("podman")
	output, clientOK := readPodman(ctx, run, "--version")
	client := strings.TrimPrefix(strings.TrimSpace(string(output)), "podman version ")
	clientMet, clientKnown := versionAtLeast(client, minimumWindowsPodman)
	report.add("client", clientPathErr != nil || clientOK && clientKnown, clientOK && clientMet, minimumWindowsPodman)
	machine := inspectMachine(ctx, run)
	report.add("running_machine", machine.StateKnown, machine.Running)
	report.add("wsl2", machine.Provider != "", machine.Provider == "wsl")
	reportMachineService(ctx, run, machine, &report)
	_, sshErr := exec.LookPath("ssh")
	report.add("ssh", true, sshErr == nil)
	_, keygenErr := exec.LookPath("ssh-keygen")
	report.add("ssh_keygen", true, keygenErr == nil)
	if machine.Running && machine.Provider == "wsl" {
		output, ok := readPodman(ctx, run, "machine", "ssh", machine.Name, "sh", "-c", "'if [ -e /etc/wsl.conf ] || [ -L /etc/wsl.conf ]; then cat /etc/wsl.conf; fi'")
		if ok {
			report.AutomountRoot, ok = automountRoot(string(output))
		}
		report.add("automount", ok, ok)
	}
	if report.Err() != nil && ctx.Err() != nil {
		report.err = ctx.Err()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			report.err = errors.New(timeoutMessage)
		}
	}
	return report
}

type machineFacts struct {
	Name       string
	Provider   string
	Running    bool
	StateKnown bool
	Rootful    *bool
}

func inspectMachine(ctx context.Context, run process.Runner) machineFacts {
	var machines []struct {
		Name    string
		Default bool
		Running bool
		VMType  string
	}
	output, ok := readPodman(ctx, run, "machine", "list", "--format", "json")
	if !ok || json.Unmarshal(output, &machines) != nil {
		return machineFacts{}
	}
	if len(machines) == 0 {
		return machineFacts{StateKnown: true}
	}
	selected := -1
	for index, machine := range machines {
		if machine.Default {
			selected = index
			break
		}
	}
	if selected < 0 && len(machines) == 1 {
		selected = 0
	}
	if selected < 0 {
		return machineFacts{}
	}
	machine := machines[selected]
	facts := machineFacts{Name: machine.Name, Provider: machine.VMType}
	if facts.Name == "" {
		return facts
	}
	var inspected []struct {
		Name    string
		State   string
		Rootful *bool
	}
	output, ok = readPodman(ctx, run, "machine", "inspect", facts.Name)
	if !ok || json.Unmarshal(output, &inspected) != nil || len(inspected) != 1 || inspected[0].Name != facts.Name || inspected[0].State == "" {
		return facts
	}
	facts.StateKnown = true
	facts.Running = machine.Running && inspected[0].State == "running"
	facts.Rootful = inspected[0].Rootful
	return facts
}

func reportMachineService(ctx context.Context, run process.Runner, machine machineFacts, report *Report) {
	var server struct{ Server struct{ Version string } }
	var info struct {
		Host struct {
			CgroupVersion     string
			CgroupControllers []string
			Security          struct{ Rootless *bool }
		}
	}
	var serverOK, infoOK bool
	if machine.Running {
		output, ok := readPodman(ctx, run, "--connection", machine.Name, "version", "--format", "json")
		serverOK = ok && json.Unmarshal(output, &server) == nil && server.Server.Version != ""
		output, ok = readPodman(ctx, run, "--connection", machine.Name, "info", "--format", "json")
		infoOK = ok && json.Unmarshal(output, &info) == nil
	}
	serverMet, serverKnown := versionAtLeast(server.Server.Version, minimumWindowsPodman)
	report.add("machine_version", serverOK && serverKnown, serverOK && serverMet, minimumWindowsPodman)
	rootlessKnown := machine.Rootful != nil && (*machine.Rootful || machine.Running && infoOK && info.Host.Security.Rootless != nil)
	rootless := rootlessKnown && !*machine.Rootful && *info.Host.Security.Rootless
	report.add("rootless", rootlessKnown, rootless)
	controllers := map[string]bool{}
	for _, controller := range info.Host.CgroupControllers {
		controllers[controller] = true
	}
	cgroupsKnown := infoOK && info.Host.CgroupVersion != ""
	report.add("cgroups", cgroupsKnown, cgroupsKnown && info.Host.CgroupVersion == "v2" && controllers["cpu"] && controllers["memory"] && controllers["pids"])
}

func readPodman(ctx context.Context, run process.Runner, args ...string) ([]byte, bool) {
	var stdout, stderr bytes.Buffer
	status, err := run(ctx, process.Request{Name: "podman", Args: args, Streams: process.Streams{Stdout: &stdout, Stderr: &stderr}})
	return stdout.Bytes(), err == nil && status == 0
}

func versionAtLeast(version, minimum string) (met, known bool) {
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	expected := strings.Split(minimum, ".")
	if len(parts) != 3 || strings.ContainsAny(version, "-+") {
		return false, false
	}
	var values [3]int
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return false, false
		}
		values[index] = value
	}
	for index, value := range values {
		threshold, _ := strconv.Atoi(expected[index])
		if value != threshold {
			return value > threshold, true
		}
	}
	return true, true
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
