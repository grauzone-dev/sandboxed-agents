package preflight

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

const MinimumPodmanVersion = "4.4.0"

type Host struct {
	Platform string
	UID      int
	Username string
	LookPath func(string) (string, error)
	ReadFile func(string) ([]byte, error)
	Writable func(string) bool
	Run      process.Runner
}

func LocalHost() Host {
	host := Host{Platform: runtime.GOOS, UID: os.Geteuid(), LookPath: exec.LookPath, ReadFile: os.ReadFile, Writable: platform.Writable, Run: platform.Run}
	if account, err := user.LookupId(strconv.Itoa(host.UID)); err == nil {
		host.Username = account.Username
	}
	return host
}

type Result struct {
	Name   string
	Met    bool
	Remedy string
}

func Check(ctx context.Context, host Host) []Result {
	if host.Platform != "linux" {
		return []Result{{Name: "Linux host", Remedy: "check currently supports Linux hosts only"}}
	}
	has := func(name string) bool { _, err := host.LookPath(name); return err == nil }
	podman := has("podman")
	version := false
	if podman {
		var stdout, stderr bytes.Buffer
		status, err := host.Run(ctx, process.Request{Name: "podman", Args: []string{"--version"}, Streams: process.Streams{Stdout: &stdout, Stderr: &stderr}})
		version = err == nil && status == 0 && supportedVersion(stdout.String())
	}
	controllers := delegatedControllers(host)
	results := []Result{
		{Name: "podman", Met: podman, Remedy: "install Podman and make sure podman is on PATH"},
		{Name: "Podman version", Met: version, Remedy: "install Podman " + MinimumPodmanVersion + " or newer"},
		{Name: "rootless", Met: host.UID > 0, Remedy: "run sandboxed-agents as your own user, not as root or with sudo"},
		{Name: "subordinate UID", Met: hasRange(host, "/etc/subuid"), Remedy: "add a subordinate UID range for your user to /etc/subuid"},
		{Name: "subordinate GID", Met: hasRange(host, "/etc/subgid"), Remedy: "add a subordinate GID range for your user to /etc/subgid"},
		{Name: "newuidmap", Met: has("newuidmap"), Remedy: "install newuidmap (in the uidmap or shadow-utils package) and make sure it is on PATH"},
		{Name: "newgidmap", Met: has("newgidmap"), Remedy: "install newgidmap (in the uidmap or shadow-utils package) and make sure it is on PATH"},
		{Name: "pasta", Met: has("pasta"), Remedy: "install pasta (the passt package) and make sure it is on PATH"},
		{Name: "cgroups v2", Met: controllers != nil, Remedy: "boot the host with the unified cgroup v2 hierarchy"},
	}
	for _, controller := range []struct{ key, name string }{{"cpu", "CPU"}, {"memory", "memory"}, {"pids", "process"}} {
		results = append(results, Result{Name: controller.name, Met: controllers[controller.key], Remedy: "delegate the cgroup v2 " + controller.key + " controller to your user, for example with Delegate= in a user@.service drop-in"})
	}
	for _, tool := range []string{"ssh", "ssh-keygen"} {
		results = append(results, Result{Name: tool, Met: has(tool), Remedy: "install the OpenSSH client and make sure " + tool + " is on PATH"})
	}
	return results
}

func Run(ctx context.Context, host Host, output io.Writer) error {
	failed := false
	for _, result := range Check(ctx, host) {
		prefix, detail := "OK", ""
		if !result.Met {
			failed, prefix, detail = true, "MISSING", ": "+result.Remedy
		}
		if _, err := fmt.Fprintf(output, "%s: %s%s\n", prefix, result.Name, detail); err != nil {
			return err
		}
	}
	if failed {
		return errors.New("host prerequisites are missing")
	}
	return nil
}

var podmanVersion = regexp.MustCompile(`^podman version ([0-9]+)\.([0-9]+)\.([0-9]+)([-+][0-9A-Za-z.-]+)?$`)

func supportedVersion(output string) bool {
	match := podmanVersion.FindStringSubmatch(strings.TrimSpace(output))
	if match == nil {
		return false
	}
	for index, component := range strings.Split(MinimumPodmanVersion, ".") {
		actual, err := strconv.ParseUint(match[index+1], 10, 32)
		if err != nil {
			return false
		}
		minimum, err := strconv.ParseUint(component, 10, 32)
		if err != nil {
			return false
		}
		if actual != minimum {
			return actual > minimum
		}
	}
	return !strings.HasPrefix(match[4], "-")
}

func hasRange(host Host, path string) bool {
	data, err := host.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(strings.TrimSpace(line), ":")
		if len(fields) != 3 || (fields[0] != strconv.Itoa(host.UID) && (host.Username == "" || fields[0] != host.Username)) {
			continue
		}
		start, startErr := strconv.ParseUint(fields[1], 10, 32)
		size, sizeErr := strconv.ParseUint(fields[2], 10, 32)
		if startErr == nil && sizeErr == nil && start > 0 && size > 0 && start+size <= 1<<32 {
			return true
		}
	}
	return false
}

func delegatedControllers(host Host) map[string]bool {
	mounts, err := host.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	membership, err := host.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil
	}
	var group string
	for _, line := range strings.Split(string(membership), "\n") {
		if strings.HasPrefix(line, "0::/") {
			group = strings.TrimPrefix(line, "0::")
			break
		}
	}
	if group == "" {
		return nil
	}
	for _, line := range strings.Split(string(mounts), "\n") {
		parts := strings.SplitN(line, " - ", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "cgroup2 ") {
			continue
		}
		fields := strings.Fields(parts[0])
		if len(fields) < 6 {
			continue
		}
		root, mount := unescapeMount(fields[3]), unescapeMount(fields[4])
		relative, err := filepath.Rel(root, group)
		if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
			continue
		}
		current := filepath.Join(mount, relative)
		available := map[string]bool{}
		var candidates []string
		for path := current; ; path = filepath.Dir(path) {
			candidates = append(candidates, path)
			if filepath.Base(path) == "user-"+strconv.Itoa(host.UID)+".slice" {
				// systemd delegates to the user manager, a sibling of the login session scope, not an ancestor of it.
				candidates = append(candidates, filepath.Join(path, "user@"+strconv.Itoa(host.UID)+".service"))
			}
			if path == mount {
				break
			}
		}
		for _, path := range candidates {
			data, err := host.ReadFile(filepath.Join(path, "cgroup.controllers"))
			if err == nil && host.Writable(path) && host.Writable(filepath.Join(path, "cgroup.procs")) && host.Writable(filepath.Join(path, "cgroup.subtree_control")) {
				for _, name := range strings.Fields(string(data)) {
					available[name] = true
				}
				return available
			}
		}
		return available
	}
	return nil
}

func unescapeMount(path string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(path)
}
