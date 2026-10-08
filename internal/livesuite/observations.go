package livesuite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

type observationMount struct {
	Type        string
	Name        string
	Source      string
	Destination string
	RW          bool
}

type observationContainer struct {
	Name   string
	Image  string
	Config struct{ Labels map[string]string }
	State  struct {
		Running bool
		Pid     int
	}
	Mounts []observationMount
}

type observedIdentity struct {
	UID               []uint64 `json:"uid"`
	GID               []uint64 `json:"gid"`
	NoNewPrivileges   int      `json:"no_new_privileges"`
	Name              string   `json:"name"`
	SameUserNamespace bool     `json:"same_user_namespace"`
	UserNamespace     string   `json:"user_namespace"`
}

type kernelObservation struct {
	Start         observedIdentity `json:"start"`
	Manager       observedIdentity `json:"manager"`
	Memory        string           `json:"memory"`
	CPU           string           `json:"cpu"`
	Pids          string           `json:"pids"`
	Shm           uint64           `json:"shm"`
	Gateway       string           `json:"gateway"`
	UserNamespace string           `json:"user_namespace"`
}

type idMap struct{ Inside, Outside, Count uint64 }
type idRange struct{ Start, Count uint64 }

type hostObservation struct {
	UID, GID           uint64
	StartUID, StartGID uint64
	UIDMap, GIDMap     []idMap
	SubUID, SubGID     []idRange
}

type sandboxObservation struct {
	Mounts                             []observationMount
	Kernel                             kernelObservation
	Agent                              observedIdentity
	UIDMap, GIDMap                     []idMap
	HostUID, HostGID, RootUID, RootGID uint64
}

func newSandboxObserver(ctx context.Context, config Config, run process.Runner, group, name, connection string) func(func(string, func() error) error, string) error {
	container := "sandboxed-agents." + group + "." + name
	var baseline *sandboxObservation
	return func(check func(string, func() error) error, phase string) error {
		probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		var record observationContainer
		if err := check(phase+"/mounts-three-named-volumes-no-binds-or-sockets", func() error {
			output, err := readObservation(probeCtx, run, "podman", []string{"container", "inspect", container}, config.Stderr)
			if err != nil {
				return err
			}
			var records []observationContainer
			if json.Unmarshal(output, &records) != nil || len(records) != 1 || records[0].Name != container || !records[0].State.Running || records[0].State.Pid < 1 || records[0].Image == "" || records[0].Config.Labels[sandbox.OwnerLabel] != group || records[0].Config.Labels[sandbox.NameLabel] != name {
				return errors.New("container-observation")
			}
			record = records[0]
			return verifyObservationMounts(record.Mounts, container)
		}); err != nil {
			return err
		}
		var host hostObservation
		var kernel kernelObservation
		var agent observedIdentity
		if err := check(phase+"/kernel-observations", func() error {
			script := fmt.Sprintf(hostObservationScript, record.State.Pid, record.State.Pid, record.State.Pid)
			executable, args := "sh", []string{"-c", script}
			if connection != "" {
				quoted := "'" + strings.ReplaceAll(script, "'", "'\\''") + "'"
				executable, args = "podman", []string{"machine", "ssh", connection, "sh", "-c", quoted}
			}
			data, err := readObservation(probeCtx, run, executable, args, config.Stderr)
			if err != nil {
				return err
			}
			host, err = parseHostObservation(string(data))
			if err != nil {
				return err
			}
			data, err = readObservation(probeCtx, run, "podman", []string{"exec", "--user=0:0", container, "node", "-e", kernelObservationScript}, config.Stderr)
			if err != nil {
				return err
			}
			if err = json.Unmarshal(data, &kernel); err != nil {
				return fmt.Errorf("kernel-json: %w", err)
			}
			data, err = readObservation(probeCtx, run, "podman", []string{"exec", "--user=1000:1000", container, "node", "-e", agentObservationScript}, config.Stderr)
			if err != nil {
				return err
			}
			if err = json.Unmarshal(data, &agent); err != nil {
				return fmt.Errorf("agent-json: %w", err)
			}
			return nil
		}); err != nil {
			return err
		}
		agent.SameUserNamespace = kernel.UserNamespace != "" && agent.UserNamespace == kernel.UserNamespace
		rootUID, rootUIDOK := mappedID(host.UIDMap, 0)
		rootGID, rootGIDOK := mappedID(host.GIDMap, 0)
		agentUID, agentUIDOK := mappedID(host.UIDMap, 1000)
		agentGID, agentGIDOK := mappedID(host.GIDMap, 1000)
		checks := []struct {
			name string
			pass bool
		}{
			{"root-maps-to-subordinate-ids", rootUIDOK && rootGIDOK && rootUID != 0 && rootGID != 0 && rootUID != host.UID && rootGID != host.GID && withinRanges(rootUID, host.SubUID) && withinRanges(rootGID, host.SubGID)},
			{"agent-uid-1000-gid-1000-maps-to-host-user", agentUIDOK && agentGIDOK && agentUID == host.UID && agentGID == host.GID && host.UID != 0 && host.GID != 0},
			{"start-uid-0-gid-0", identityIs(kernel.Start, 0) && host.StartUID == rootUID && host.StartGID == rootGID},
			{"manager-exec-uid-0-gid-0", identityIs(kernel.Manager, 0)},
			{"shell-uid-1000-gid-1000", identityIs(agent, 1000) && agent.Name == "agent" && agent.SameUserNamespace},
			{"no-new-privileges", kernel.Start.NoNewPrivileges == 1 && kernel.Manager.NoNewPrivileges == 1 && agent.NoNewPrivileges == 1},
			{"memory-limit-256m", strings.TrimSpace(kernel.Memory) == "268435456"},
			{"cpu-limit-1", observedCPUIsOne(kernel.CPU)},
			{"process-limit-128", strings.TrimSpace(kernel.Pids) == "128"},
			{"shm-limit-16m", kernel.Shm == 16777216},
		}
		var result error
		for _, item := range checks {
			result = errors.Join(result, check(phase+"/"+item.name, func() error {
				if !item.pass {
					return errors.New(item.name)
				}
				return nil
			}))
		}
		if result != nil {
			return result
		}
		if err := check(phase+"/gateway-host-access-blocked-with-positive-control", func() error {
			return observeGateway(probeCtx, config, run, group, name, record.Image, container, kernel.Gateway)
		}); err != nil {
			return err
		}
		kernel.UserNamespace = ""
		agent.UserNamespace = ""
		sort.Slice(record.Mounts, func(i, j int) bool { return record.Mounts[i].Destination < record.Mounts[j].Destination })
		observed := sandboxObservation{Mounts: record.Mounts, Kernel: kernel, Agent: agent, UIDMap: host.UIDMap, GIDMap: host.GIDMap, HostUID: host.UID, HostGID: host.GID, RootUID: rootUID, RootGID: rootGID}
		if baseline == nil {
			baseline = &observed
			return nil
		}
		return check(phase+"/observations-match-created", func() error {
			if !reflect.DeepEqual(*baseline, observed) {
				return errors.New("observations-changed")
			}
			return nil
		})
	}
}

func identityIs(identity observedIdentity, id uint64) bool {
	if len(identity.UID) != 4 || len(identity.GID) != 4 {
		return false
	}
	for _, values := range [][]uint64{identity.UID, identity.GID} {
		for _, value := range values {
			if value != id {
				return false
			}
		}
	}
	return true
}

func observedCPUIsOne(value string) bool {
	parts := strings.Fields(value)
	if len(parts) != 2 {
		return false
	}
	quota, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || quota == 0 {
		return false
	}
	period, err := strconv.ParseUint(parts[1], 10, 64)
	return err == nil && period != 0 && quota == period
}

func mappedID(mappings []idMap, id uint64) (uint64, bool) {
	for _, mapping := range mappings {
		if id >= mapping.Inside && id-mapping.Inside < mapping.Count {
			return mapping.Outside + id - mapping.Inside, true
		}
	}
	return 0, false
}

func withinRanges(id uint64, ranges []idRange) bool {
	for _, r := range ranges {
		if id >= r.Start && id-r.Start < r.Count {
			return true
		}
	}
	return false
}

func parseHostObservation(data string) (hostObservation, error) {
	var host hostObservation
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return host, errors.New("host-kernel-facts")
		}
		values := make([]uint64, len(parts)-1)
		for i, text := range parts[1:] {
			number, err := strconv.ParseUint(text, 10, 32)
			if err != nil {
				return host, errors.New("host-kernel-facts")
			}
			values[i] = number
		}
		switch parts[0] {
		case "host-uid", "host-gid", "start-host-uid", "start-host-gid":
			if len(values) != 1 || seen[parts[0]] {
				return host, errors.New("host-kernel-facts")
			}
			seen[parts[0]] = true
			switch parts[0] {
			case "host-uid":
				host.UID = values[0]
			case "host-gid":
				host.GID = values[0]
			case "start-host-uid":
				host.StartUID = values[0]
			case "start-host-gid":
				host.StartGID = values[0]
			}
		case "uid-map", "gid-map":
			if len(values) != 3 || values[2] == 0 || values[0]+values[2] > 1<<32 || values[1]+values[2] > 1<<32 {
				return host, errors.New("host-id-map")
			}
			mapping := idMap{values[0], values[1], values[2]}
			if parts[0] == "uid-map" {
				host.UIDMap = append(host.UIDMap, mapping)
			} else {
				host.GIDMap = append(host.GIDMap, mapping)
			}
		case "sub-uid", "sub-gid":
			if len(values) != 2 || values[1] == 0 || values[0]+values[1] > 1<<32 {
				return host, errors.New("host-subordinate-range")
			}
			r := idRange{values[0], values[1]}
			if parts[0] == "sub-uid" {
				host.SubUID = append(host.SubUID, r)
			} else {
				host.SubGID = append(host.SubGID, r)
			}
		default:
			return host, errors.New("host-kernel-facts")
		}
	}
	if len(seen) != 4 || len(host.UIDMap) == 0 || len(host.GIDMap) == 0 || len(host.SubUID) == 0 || len(host.SubGID) == 0 {
		return host, errors.New("host-kernel-facts")
	}
	for _, mappings := range [][]idMap{host.UIDMap, host.GIDMap} {
		sort.Slice(mappings, func(i, j int) bool { return mappings[i].Inside < mappings[j].Inside })
		for i, a := range mappings {
			for _, b := range mappings[i+1:] {
				if a.Inside+a.Count > b.Inside || a.Outside < b.Outside+b.Count && b.Outside < a.Outside+a.Count {
					return host, errors.New("host-id-map-overlap")
				}
			}
		}
	}
	return host, nil
}

const hostObservationScript = `set -eu
printf 'host-uid %%s\nhost-gid %%s\n' "$(id -u)" "$(id -g)"
awk '{print "uid-map",$0}' /proc/%d/uid_map
awk '{print "gid-map",$0}' /proc/%d/gid_map
awk '/^Uid:/{print "start-host-uid",$3} /^Gid:/{print "start-host-gid",$3}' /proc/%d/status
awk -F: -v name="$(id -un)" -v uid="$(id -u)" '$1==name || $1==uid {print "sub-uid",$2,$3}' /etc/subuid
awk -F: -v name="$(id -un)" -v uid="$(id -u)" '$1==name || $1==uid {print "sub-gid",$2,$3}' /etc/subgid`

func readObservation(ctx context.Context, run process.Runner, name string, args []string, stderr io.Writer) ([]byte, error) {
	var output bytes.Buffer
	status, err := run(ctx, process.Request{Name: name, Args: args, Streams: process.Streams{Stdout: &output, Stderr: stderr}})
	if err != nil {
		return nil, fmt.Errorf("observation-process: %w", err)
	}
	if status != 0 {
		return nil, fmt.Errorf("observation-status-%d", status)
	}
	return output.Bytes(), nil
}

func verifyObservationMounts(mounts []observationMount, container string) error {
	want := map[string]string{"/workspace": container + ".workspace", "/home/agent": container + ".home", "/etc/ssh": container + ".ssh"}
	if len(mounts) != 3 {
		return errors.New("mount-inventory")
	}
	for _, mount := range mounts {
		if mount.Type != "volume" || want[mount.Destination] == "" || mount.Name != want[mount.Destination] || mount.Source == "" || !mount.RW {
			return errors.New("mount-inventory")
		}
		delete(want, mount.Destination)
	}
	return nil
}
