package livesuite

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

var privateImageIDPattern = regexp.MustCompile(`^(sha256:)?[0-9a-f]{64}$`)

func sameImageID(first, second string) bool {
	return strings.TrimPrefix(first, "sha256:") == strings.TrimPrefix(second, "sha256:")
}

func verifyPrivateUpdateImage(ctx context.Context, podman process.Runner, id, marker string) error {
	var records []struct {
		ID       string
		RepoTags []string
		Config   struct{ Labels map[string]string }
		Labels   map[string]string
	}
	if err := liveJSON(ctx, podman, []string{"image", "inspect", id}, &records); err != nil {
		return err
	}
	if len(records) != 1 || !sameImageID(records[0].ID, id) {
		return errors.New("update-private-image-identity")
	}
	record := records[0]
	labels := record.Labels
	if labels == nil {
		labels = record.Config.Labels
	}
	if labels["io.github.sandboxed-agents.live-fixture"] != marker {
		return errors.New("update-private-image-owner")
	}
	for _, tag := range record.RepoTags {
		if tag != "<none>:<none>" {
			return errors.New("update-private-image-tagged")
		}
	}
	return nil
}

func updatesChecksNotRun() []Check {
	checks := []Check{{Name: "updates", Result: "not-run"}}
	for _, scenario := range []string{"success", "rollback"} {
		prefix := "updates/" + scenario
		checks = append(checks, Check{Name: prefix, Result: "not-run"})
		steps := []string{"standard-account", "client", "target", "group-empty", "host-clean", "up", "created/loopback", "created/config", "created/connect", "created/setup-snapshot", "data-written", "outdated", "fixture-ready", "fixture-restored", "cleanup", "fixture-cleanup"}
		if scenario == "success" {
			steps = append(steps, "update", "preserved", "updated/setup-unchanged", "updated/loopback", "updated/config", "updated/connect")
		} else {
			steps = append(steps, "forced-failure", "restored", "data-preserved")
		}
		for _, step := range steps {
			checks = append(checks, Check{Name: prefix + "/" + step, Result: "not-run"})
		}
	}
	return checks
}

func liveCommandEnvironment(config Config, group string) []string {
	env := withGroup(os.Environ(), group)
	if config.Host.OS == "windows" {
		return scrubPodmanRemoteEnvironment(env)
	}
	return env
}

func runUpdates(ctx context.Context, config Config, run process.Runner, executable, group, assetHash string, check func(string, func() error) error) error {
	if err := check("updates/success", func() error { return runUpdateScenario(ctx, config, run, executable, group, assetHash, check, false) }); err != nil {
		return err
	}
	return check("updates/rollback", func() error { return runUpdateScenario(ctx, config, run, executable, group, assetHash, check, true) })
}

// updateContainer keeps independent observations of the configuration that
// update promises to preserve. Image, ID and runtime state are compared separately.
type updateContainer struct {
	ID, Name, Image string
	State           struct {
		Running bool
		Status  string
	}
	Config     struct{ Labels map[string]string }
	Mounts     []observationMount
	HostConfig struct {
		Memory, NanoCpus, CpuPeriod, CpuQuota, PidsLimit, ShmSize int64
		PortBindings                                              map[string][]struct{ HostIP, HostPort string }
	}
}

func readUpdateContainer(ctx context.Context, podman process.Runner, group, name string) (updateContainer, error) {
	var records []updateContainer
	container := "sandboxed-agents." + group + "." + name
	if err := liveJSON(ctx, podman, []string{"container", "inspect", container}, &records); err != nil {
		return updateContainer{}, err
	}
	if len(records) != 1 {
		return updateContainer{}, errors.New("update-container-inventory")
	}
	record := records[0]
	labels := record.Config.Labels
	if record.ID == "" || record.Image == "" || record.Name != container || record.State.Status == "" || labels[sandbox.OwnerLabel] != group || labels[sandbox.NameLabel] != name {
		return record, errors.New("update-container-identity")
	}
	if err := verifyObservationMounts(record.Mounts, container); err != nil {
		return record, err
	}
	sort.Slice(record.Mounts, func(i, j int) bool { return record.Mounts[i].Destination < record.Mounts[j].Destination })
	expected := map[string]string{images.ToolchainsLabel: "", sandbox.WorkspaceKindLabel: "volume", sandbox.MemoryLabel: "268435456", sandbox.CPUsLabel: "1", sandbox.PIDsLimitLabel: "128", sandbox.ShmSizeLabel: "16777216"}
	for key, value := range expected {
		actual, present := labels[key]
		if !present || actual != value {
			return record, errors.New("update-recorded-configuration")
		}
	}
	bindings := record.HostConfig.PortBindings["22/tcp"]
	if len(record.HostConfig.PortBindings) != 1 || len(bindings) != 1 || bindings[0].HostIP != "127.0.0.1" || bindings[0].HostPort != labels[sandbox.SSHPortLabel] || bindings[0].HostPort == "" {
		return record, errors.New("update-port-configuration")
	}
	host := record.HostConfig
	cpuOne := host.NanoCpus == 1000000000 || host.NanoCpus == 0 && host.CpuPeriod > 0 && host.CpuQuota == host.CpuPeriod
	if host.Memory != 268435456 || !cpuOne || host.PidsLimit != 128 || host.ShmSize != 16777216 {
		return record, errors.New("update-resource-configuration")
	}
	return record, nil
}

func sameUpdateConfiguration(a, b updateContainer) bool {
	if !reflect.DeepEqual(a.Mounts, b.Mounts) || !reflect.DeepEqual(a.HostConfig, b.HostConfig) {
		return false
	}
	for _, key := range []string{sandbox.OwnerLabel, sandbox.NameLabel, images.ToolchainsLabel, sandbox.WorkspaceKindLabel, sandbox.SSHPortLabel, sandbox.MemoryLabel, sandbox.CPUsLabel, sandbox.PIDsLimitLabel, sandbox.ShmSizeLabel} {
		if a.Config.Labels[key] != b.Config.Labels[key] {
			return false
		}
	}
	return true
}

func runUpdateScenario(ctx context.Context, config Config, run process.Runner, executable, group, assetHash string, check func(string, func() error) error, rollback bool) (result error) {
	prefix := "updates/success"
	afterPhase := "updated"
	if rollback {
		prefix = "updates/rollback"
		afterPhase = ""
	}
	var privateImage, fixtureMarker string
	var cleanupPodman process.Runner
	// runSSHScenario cleans up the sandbox before this private image is removed.
	defer func() {
		if privateImage == "" {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		defer cancel()
		result = errors.Join(result, check(prefix+"/fixture-cleanup", func() error {
			if err := requireEmptyGroup(cleanup, cleanupPodman, group); err != nil {
				return err
			}
			if err := verifyPrivateUpdateImage(cleanup, cleanupPodman, privateImage, fixtureMarker); err != nil {
				return err
			}
			_, err := readObservation(cleanup, cleanupPodman, "podman", []string{"image", "rm", "--no-prune", privateImage}, nil)
			return err
		}))
	}()
	return runSSHScenario(ctx, config, run, executable, group, check, sshScenario{prefix: prefix, afterPhase: afterPhase, recheckPodmanTarget: true, beforeRemove: func(ctx context.Context, name string, podman process.Runner, command liveExecutable) error {
		return cleanupUpdateBackup(ctx, config, podman, command, group, name)
	}, transition: func(ctx context.Context, name string, podman process.Runner, command liveExecutable) error {
		cleanupPodman = podman
		invoke := func(ctx context.Context, args ...string) error {
			return command.invoke(ctx, process.Request{Args: args, Env: liveCommandEnvironment(config, group), Streams: process.Streams{Stdout: config.Stdout, Stderr: config.Stderr}})
		}
		var baseline updateContainer
		var volumes []lifecycleVolume
		token := rand.Text()
		if err := check(prefix+"/data-written", func() error {
			var err error
			baseline, err = readUpdateContainer(ctx, podman, group, name)
			if err != nil {
				return err
			}
			volumes, err = readLifecycleVolumes(ctx, podman, group, name)
			if err != nil {
				return err
			}
			_, err = readObservation(ctx, podman, "podman", []string{"exec", "--user=0:0", baseline.Name, "node", "-e", updateWriteDataScript, token}, nil)
			return err
		}); err != nil {
			return err
		}
		var currentImage string
		if err := check(prefix+"/outdated", func() (result error) {
			var current []struct{ ID string }
			if err := liveJSON(ctx, podman, []string{"image", "inspect", images.BaseTag(assetHash)}, &current); err != nil {
				return err
			}
			if len(current) != 1 || current[0].ID == "" {
				return errors.New("update-current-image")
			}
			currentImage = current[0].ID
			if err := invoke(ctx, "stop", name); err != nil {
				return err
			}
			output, err := readObservation(ctx, podman, "podman", []string{"commit", "--quiet", "--include-volumes=false", "--change", "LABEL io.github.sandboxed-agents.live-fixture=" + name, baseline.Name}, nil)
			if err != nil {
				return err
			}
			candidate := strings.TrimSpace(string(output))
			if !privateImageIDPattern.MatchString(candidate) || sameImageID(candidate, currentImage) {
				return errors.New("update-private-image")
			}
			privateImage = candidate
			fixtureMarker = name
			if err := verifyPrivateUpdateImage(ctx, podman, privateImage, fixtureMarker); err != nil {
				return err
			}
			backup := "sandboxed-agents-backup." + group + "." + name
			if _, err := readObservation(ctx, podman, "podman", []string{"rename", baseline.Name, backup}, nil); err != nil {
				return err
			}
			renamed := true
			defer func() {
				if !renamed {
					return
				}
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				defer cancel()
				result = errors.Join(result, check(prefix+"/fixture-restored", func() error {
					status, err := podman(cleanup, process.Request{Name: "podman", Args: []string{"container", "exists", baseline.Name}})
					if err != nil {
						return err
					}
					if status == 0 {
						replacement, err := readUpdateContainer(cleanup, podman, group, name)
						if err != nil {
							return err
						}
						if replacement.ID != baseline.ID {
							if !sameImageID(replacement.Image, privateImage) {
								return errors.New("update-fixture-replacement-owner")
							}
							if _, err := readObservation(cleanup, podman, "podman", []string{"rm", "--force", baseline.Name}, nil); err != nil {
								return err
							}
						}
					} else if status != 1 {
						return errors.New("update-fixture-container-exists")
					}
					_, err = readObservation(cleanup, podman, "podman", []string{"rename", backup, baseline.Name}, nil)
					return err
				}))
			}()
			if _, err := readObservation(ctx, podman, "podman", updateFixtureCreateArguments(baseline, privateImage), nil); err != nil {
				return err
			}
			if _, err := readObservation(ctx, podman, "podman", []string{"rm", backup}, nil); err != nil {
				return err
			}
			renamed = false
			if err := invoke(ctx, "start", name); err != nil {
				return err
			}
			original, err := readUpdateContainer(ctx, podman, group, name)
			if err != nil {
				return err
			}
			if !original.State.Running || !sameImageID(original.Image, privateImage) || !sameUpdateConfiguration(baseline, original) {
				return errors.New("update-outdated-fixture")
			}
			baseline = original
			return nil
		}); err != nil {
			return err
		}
		if err := check(prefix+"/fixture-ready", func() error {
			port, err := strconv.Atoi(baseline.Config.Labels[sandbox.SSHPortLabel])
			if err != nil {
				return err
			}
			return sandbox.WaitReady(ctx, baseline.Name, port, podman)
		}); err != nil {
			return err
		}
		if rollback {
			if err := invoke(ctx, "stop", name); err != nil {
				return err
			}
			stopped, err := readUpdateContainer(ctx, podman, group, name)
			if err != nil {
				return err
			}
			if stopped.State.Running || stopped.ID != baseline.ID {
				return errors.New("update-rollback-fixture-state")
			}
			baseline = stopped
			var listener net.Listener
			if err := check(prefix+"/forced-failure", func() error {
				var err error
				listener, err = net.Listen("tcp4", "127.0.0.1:"+baseline.Config.Labels[sandbox.SSHPortLabel])
				if err != nil {
					return err
				}
				defer listener.Close()
				var diagnostic, output bytes.Buffer
				err = command.invoke(ctx, process.Request{Args: []string{"update", name}, Env: liveCommandEnvironment(config, group), Streams: process.Streams{Stdout: &output, Stderr: &diagnostic}})
				if ctx.Err() != nil {
					return ctx.Err()
				}
				message := diagnostic.String()
				failedAfterCreate := strings.Contains(message, `failed at step "start the new container"`) || strings.Contains(message, `failed at step "readiness wait of the new container"`)
				if err == nil || !failedAfterCreate || !strings.Contains(message, "was restored:") || strings.Contains(message, "was not restored") {
					return errors.New("update-forced-rollback-not-observed")
				}
				return nil
			}); err != nil {
				return err
			}
			if err := check(prefix+"/restored", func() error {
				restored, err := readUpdateContainer(ctx, podman, group, name)
				if err != nil {
					return err
				}
				if restored.ID != baseline.ID || restored.Name != baseline.Name || restored.Image != baseline.Image || restored.State != baseline.State || !sameUpdateConfiguration(baseline, restored) {
					return errors.New("update-original-not-restored")
				}
				if err := requireNoUpdateBackup(ctx, podman, group, name); err != nil {
					return err
				}
				current, err := readLifecycleVolumes(ctx, podman, group, name)
				if err != nil {
					return err
				}
				if !slices.Equal(current, volumes) {
					return errors.New("update-volumes-not-preserved")
				}
				return nil
			}); err != nil {
				return err
			}
			// Restore was observed while stopped. Restart only to read the preserved
			// files through Podman, then put the sandbox back into that state.
			if err := invoke(ctx, "start", name); err != nil {
				return err
			}
			if err := check(prefix+"/data-preserved", func() error { return verifyUpdateData(ctx, podman, baseline.Name, token) }); err != nil {
				return err
			}
			return invoke(ctx, "stop", name)
		}
		if err := check(prefix+"/update", func() error { return invoke(ctx, "update", name) }); err != nil {
			return err
		}
		return check(prefix+"/preserved", func() error {
			updated, err := readUpdateContainer(ctx, podman, group, name)
			if err != nil {
				return err
			}
			if updated.ID == baseline.ID || !sameImageID(updated.Image, currentImage) || !updated.State.Running || !sameUpdateConfiguration(baseline, updated) {
				return errors.New("update-configuration-not-preserved")
			}
			if err := requireNoUpdateBackup(ctx, podman, group, name); err != nil {
				return err
			}
			current, err := readLifecycleVolumes(ctx, podman, group, name)
			if err != nil {
				return err
			}
			if !slices.Equal(current, volumes) {
				return errors.New("update-volumes-not-preserved")
			}
			return verifyUpdateData(ctx, podman, baseline.Name, token)
		})
	}})
}

// Recreate the known fixture explicitly: Podman's container clone is unavailable
// to the Windows remote client. The observed result is checked independently.
func updateFixtureCreateArguments(baseline updateContainer, image string) []string {
	args := []string{"create", "--pull=never", "--name", baseline.Name, "--publish", "127.0.0.1:" + baseline.Config.Labels[sandbox.SSHPortLabel] + ":22", "--userns=keep-id:uid=1000,gid=1000", "--user=0:0", "--security-opt=no-new-privileges", "--network=pasta:--no-map-gw", "--memory=256m", "--cpus=1", "--pids-limit=128", "--shm-size=16m"}
	for _, key := range []string{sandbox.OwnerLabel, sandbox.NameLabel, images.ToolchainsLabel, sandbox.WorkspaceKindLabel, sandbox.SSHPortLabel, sandbox.MemoryLabel, sandbox.CPUsLabel, sandbox.PIDsLimitLabel, sandbox.ShmSizeLabel} {
		args = append(args, "--label", key+"="+baseline.Config.Labels[key])
	}
	for _, mount := range baseline.Mounts {
		args = append(args, "--mount", "type=volume,source="+mount.Name+",target="+mount.Destination)
	}
	return append(args, image)
}

func cleanupUpdateBackup(ctx context.Context, config Config, podman process.Runner, command liveExecutable, group, name string) error {
	exists, err := updateBackupExists(ctx, podman, group, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	// Only update understands interrupted update recovery and checks ownership.
	// A recovered original may report nonzero; observe that the backup is gone.
	var output, diagnostic bytes.Buffer
	recoveryErr := command.invoke(ctx, process.Request{Args: []string{"update", name}, Env: liveCommandEnvironment(config, group), Streams: process.Streams{Stdout: &output, Stderr: &diagnostic}})
	if err := requireNoUpdateBackup(ctx, podman, group, name); err != nil {
		return errors.Join(recoveryErr, err)
	}
	return nil
}

func verifyUpdateData(ctx context.Context, podman process.Runner, container, token string) error {
	var data map[string]string
	if err := liveJSON(ctx, podman, []string{"exec", "--user=0:0", container, "node", "-e", updateReadDataScript}, &data); err != nil {
		return err
	}
	if len(data) != 3 {
		return errors.New("update-volume-data")
	}
	for _, path := range []string{"/home/agent", "/etc/ssh", "/workspace"} {
		if data[path] != token {
			return errors.New("update-volume-data")
		}
	}
	return nil
}

func updateBackupExists(ctx context.Context, podman process.Runner, group, name string) (bool, error) {
	status, err := podman(ctx, process.Request{Name: "podman", Args: []string{"container", "exists", "sandboxed-agents-backup." + group + "." + name}})
	if err != nil {
		return false, err
	}
	switch status {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("update-backup-inventory-status-%d", status)
	}
}

func requireNoUpdateBackup(ctx context.Context, podman process.Runner, group, name string) error {
	exists, err := updateBackupExists(ctx, podman, group, name)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("update-backup-remains")
	}
	return nil
}

const updateWriteDataScript = `const fs=require('node:fs');for(const path of ['/home/agent','/etc/ssh','/workspace'])fs.writeFileSync(path+'/.sandboxed-agents-live-data',process.argv[1]);`
const updateReadDataScript = `const fs=require('node:fs');const data={};for(const path of ['/home/agent','/etc/ssh','/workspace'])data[path]=fs.readFileSync(path+'/.sandboxed-agents-live-data','utf8');process.stdout.write(JSON.stringify(data));`
