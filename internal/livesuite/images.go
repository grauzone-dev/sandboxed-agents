package livesuite

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/preflight"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

type liveImageRecord struct {
	ID       string `json:"Id"`
	RepoTags []string
	RootFS   struct{ Layers []string }
}

type liveImageCase struct {
	kind, name, tag string
	before          liveImageRecord
	after           liveImageRecord
}

func runImages(ctx context.Context, config Config, run process.Runner, executable, group, assetHash string, check func(string, func() error) error, markImagePartRan func()) (result error) {
	probeRun := func(current context.Context, request process.Request) (int, error) {
		environment := request.Env
		if environment == nil {
			environment = os.Environ()
		}
		request.Env = scrubPodmanRemoteEnvironment(environment)
		return run(current, request)
	}
	var podman process.Runner
	var connection string
	if err := check("images/target", func() error {
		var err error
		podman, connection, err = newLivePodman(ctx, config, probeRun)
		return err
	}); err != nil {
		return err
	}
	if err := check("images/group-empty", func() error { return requireEmptyGroup(ctx, podman, group) }); err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "sandboxed-agents-live-images-*")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(directory)) }()
	environment := liveImageEnvironment(os.Environ(), group, directory)
	invoke := func(current context.Context, args []string, input io.Reader, output io.Writer) error {
		if connection != "" {
			selected, cancel := context.WithTimeout(current, 30*time.Second)
			target, err := preflight.SelectWindowsConnection(selected, podman)
			cancel()
			if err != nil {
				return err
			}
			if target != connection {
				return errors.New(changedPodmanTargetMessage)
			}
		}
		status, err := run(current, process.Request{Name: executable, Args: args, Env: environment, Streams: process.Streams{Stdin: input, Stdout: output, Stderr: config.Stderr}})
		if err != nil {
			return err
		}
		if status != 0 {
			return fmt.Errorf("%s: exit status %d", args[0], status)
		}
		return nil
	}
	var attempted []*liveImageCase
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		defer cancel()
		result = errors.Join(result, check("images/cleanup", func() error {
			var failures error
			for _, item := range attempted {
				exists, err := liveImageSandboxExists(cleanup, podman, group, item.name)
				if err != nil {
					failures = errors.Join(failures, err)
					continue
				}
				if exists {
					failures = errors.Join(failures, invoke(cleanup, []string{"remove", item.name, "--volumes"}, nil, config.Stdout))
				}
			}
			return errors.Join(failures, requireEmptyGroup(cleanup, podman, group))
		}))
	}()
	suffix := strings.ToLower(rand.Text())
	start := func(item *liveImageCase, selection ...string) error {
		return check("images/"+item.kind+"/up", func() error {
			if err := invoke(ctx, append([]string{"up", item.name}, selection...), nil, config.Stdout); err != nil {
				return err
			}
			var err error
			item.before, err = readLiveImage(ctx, podman, item.tag, item.tag, group, true)
			if err != nil {
				return err
			}
			return verifyLiveImageSandbox(ctx, podman, group, item, item.before.ID)
		})
	}
	var cases []*liveImageCase
	for _, definition := range toolchains.Catalog() {
		if !definition.Delivered {
			continue
		}
		set, err := toolchains.Parse(definition.Name)
		if err != nil {
			return err
		}
		item := &liveImageCase{kind: definition.Name, name: "images-" + definition.Name + "-" + suffix, tag: images.Tag(assetHash, set)}
		cases = append(cases, item)
		attempted = append(attempted, item)
		if err := start(item, "--with", definition.Name); err != nil {
			return err
		}
		if err := check("images/"+item.kind+"/smoke", func() error {
			return invoke(ctx, []string{"shell", item.name}, strings.NewReader(smokeCheckScript(definition)), config.Stdout)
		}); err != nil {
			return err
		}
		if err := check("images/temporary-contexts", func() error { return verifyLiveImageContexts(directory) }); err != nil {
			return err
		}
	}
	base := &liveImageCase{kind: "base", name: "images-base-" + suffix, tag: images.BaseTag(assetHash)}
	cases = append(cases, base)
	attempted = append(attempted, base)
	if err := start(base); err != nil {
		return err
	}
	if err := check("images/base/contents", func() error {
		return invoke(ctx, []string{"shell", base.name}, strings.NewReader(baseImageCheckScript), config.Stdout)
	}); err != nil {
		return err
	}
	if err := check("images/base/host-keys", func() error {
		readHostKeys := func(item *liveImageCase) (map[string]string, error) {
			if err := verifyLiveImageSandbox(ctx, podman, group, item, item.before.ID); err != nil {
				return nil, err
			}
			var output bytes.Buffer
			if err := invoke(ctx, []string{"shell", item.name}, strings.NewReader(hostKeysCheckScript), &output); err != nil {
				return nil, err
			}
			var keys map[string]string
			if err := json.Unmarshal(output.Bytes(), &keys); err != nil {
				return nil, err
			}
			if len(keys) == 0 {
				return nil, errors.New(imageHostKeysEmptyMessage)
			}
			for kind, key := range keys {
				if kind == "" || strings.TrimSpace(key) == "" {
					return nil, errors.New(imageHostKeysInvalidMessage)
				}
			}
			return keys, nil
		}
		baseKeys, err := readHostKeys(base)
		if err != nil {
			return err
		}
		toolchainKeys, err := readHostKeys(cases[0])
		if err != nil {
			return err
		}
		for _, key := range baseKeys {
			for _, other := range toolchainKeys {
				if key == other {
					return errors.New(imageHostKeysSharedMessage)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := check("images/layers-before", func() error { return verifyLiveImageLayers(cases, base, false) }); err != nil {
		return err
	}
	if err := check("images/temporary-contexts", func() error { return verifyLiveImageContexts(directory) }); err != nil {
		return err
	}
	if err := check("images/build", func() error {
		var output bytes.Buffer
		if err := invoke(ctx, []string{"build"}, nil, io.MultiWriter(&output, liveImageOutput(config.Stdout))); err != nil {
			return err
		}
		markImagePartRan()
		text := strings.Join(strings.Fields(strings.ToLower(output.String())), " ")
		if !strings.Contains(text, "existing sandboxes keep their current image until you update them") || !strings.Contains(text, "list marks them as outdated") {
			return errors.New(imageRebuildNoteMessage)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, item := range cases {
		if err := check("images/"+item.kind+"/rebuild", func() error {
			var err error
			item.after, err = readLiveImage(ctx, podman, item.tag, item.tag, group, true)
			if err != nil {
				return err
			}
			if item.after.ID == item.before.ID {
				return errors.New(imageNotRebuiltMessage)
			}
			retained, err := readLiveImage(ctx, podman, item.before.ID, item.tag, group, false)
			if err != nil {
				return err
			}
			if retained.ID != item.before.ID {
				return errors.New(imageNotRetainedMessage)
			}
			return verifyLiveImageSandbox(ctx, podman, group, item, item.before.ID)
		}); err != nil {
			return err
		}
	}
	if err := check("images/layers-after", func() error { return verifyLiveImageLayers(cases, base, true) }); err != nil {
		return err
	}
	if err := check("images/outdated", func() error {
		var output bytes.Buffer
		if err := invoke(ctx, []string{"list"}, nil, &output); err != nil {
			return err
		}
		for _, item := range cases {
			if item.kind != "base" {
				if err := verifyLifecycleList(output.String(), item.name, "running (outdated)"); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return check("images/temporary-contexts", func() error { return verifyLiveImageContexts(directory) })
}

func liveImageOutput(output io.Writer) io.Writer {
	if output == nil {
		return io.Discard
	}
	return output
}

func liveImageEnvironment(original []string, group, directory string) []string {
	var result []string
	for _, value := range withGroup(scrubPodmanRemoteEnvironment(original), group) {
		key, _, _ := strings.Cut(value, "=")
		switch strings.ToUpper(key) {
		case "TMPDIR", "TMP", "TEMP":
		default:
			result = append(result, value)
		}
	}
	return append(result, "TMPDIR="+directory, "TMP="+directory, "TEMP="+directory)
}

func readLiveImage(ctx context.Context, run process.Runner, reference, expectedTag, group string, tagged bool) (liveImageRecord, error) {
	var records []liveImageRecord
	if err := liveJSON(ctx, run, []string{"image", "inspect", reference}, &records); err != nil {
		return liveImageRecord{}, err
	}
	if len(records) != 1 || records[0].ID == "" || len(records[0].RootFS.Layers) == 0 {
		return liveImageRecord{}, errors.New(imageInspectInvalidMessage)
	}
	record := records[0]
	for _, layer := range record.RootFS.Layers {
		if layer == "" {
			return liveImageRecord{}, errors.New(imageLayersInvalidMessage)
		}
	}
	if tagged && !slices.Contains(record.RepoTags, reference) {
		return liveImageRecord{}, errors.New(imageTagMissingMessage)
	}
	for _, tag := range record.RepoTags {
		if tag != expectedTag && strings.Contains(tag, group) {
			return liveImageRecord{}, errors.New(imageGroupNameMessage)
		}
	}
	return record, nil
}

func verifyLiveImageSandbox(ctx context.Context, run process.Runner, group string, item *liveImageCase, id string) error {
	var records []struct {
		Name, Image, ImageName string
		Config                 struct{ Labels map[string]string }
		State                  struct{ Running bool }
		Mounts                 []observationMount
	}
	name := "sandboxed-agents." + group + "." + item.name
	if err := liveJSON(ctx, run, []string{"container", "inspect", name}, &records); err != nil {
		return err
	}
	if len(records) != 1 {
		return errors.New(imageSandboxInspectInvalidMessage)
	}
	record := records[0]
	if record.Name != name || !record.State.Running || record.Image != id || record.Config.Labels[sandbox.OwnerLabel] != group || record.Config.Labels[sandbox.NameLabel] != item.name {
		return errors.New(imageSandboxStateMessage)
	}
	if record.ImageName != item.tag && record.ImageName != id {
		return errors.New(imageGroupNameMessage)
	}
	ssh := 0
	for _, mount := range record.Mounts {
		if mount.Destination == "/etc/ssh" {
			ssh++
			if mount.Type != "volume" || mount.Name != name+".ssh" || !mount.RW {
				return errors.New(imageSshVolumeMessage)
			}
		}
	}
	if ssh != 1 {
		return errors.New(imageSshVolumeMessage)
	}
	return nil
}

func verifyLiveImageLayers(cases []*liveImageCase, base *liveImageCase, after bool) error {
	layers := base.before.RootFS.Layers
	if after {
		layers = base.after.RootFS.Layers
	}
	for _, item := range cases {
		if item == base {
			continue
		}
		current := item.before.RootFS.Layers
		if after {
			current = item.after.RootFS.Layers
		}
		if len(current) < len(layers) || !slices.Equal(current[:len(layers)], layers) {
			return errors.New(imageBaseLayersMessage)
		}
	}
	return nil
}

func verifyLiveImageContexts(directory string) error {
	return filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), "sandboxed-agents-context-") || strings.HasPrefix(entry.Name(), "sandboxed-agents-toolchains-") {
			return errors.New(imageContextRetainedMessage)
		}
		return nil
	})
}

func liveImageSandboxExists(ctx context.Context, run process.Runner, group, name string) (bool, error) {
	base := "sandboxed-agents." + group + "." + name
	objects := [][]string{{"container", "exists", base}, {"volume", "exists", base + ".home"}, {"volume", "exists", base + ".workspace"}, {"volume", "exists", base + ".ssh"}}
	for _, args := range objects {
		status, err := run(ctx, process.Request{Name: "podman", Args: args})
		if err != nil {
			return false, err
		}
		if status == 0 {
			return true, nil
		}
		if status != 1 {
			return false, errors.New(imageCleanupInventoryMessage)
		}
	}
	return false, nil
}
