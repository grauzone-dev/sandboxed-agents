package sandbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

func UpdateAll(ctx context.Context, hostOS, group, assetHash string, options UpdateOptions, run process.Runner, streams process.Streams) error {
	names, err := discoverGroupSandboxes(ctx, group, run)
	if err != nil {
		return err
	}
	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	failed := false
	report := func(name string, err error) {
		failed = true
		fmt.Fprintln(streams.Stderr, fmt.Errorf(updateAllSkippedFormat, name, err))
	}
	var updates []*Update
	type imageJob struct {
		set     toolchains.Set
		updates []*Update
	}
	var jobs []imageJob
	jobIndex := make(map[string]int)
	for _, name := range names {
		release, err := LockLifecycle(hostOS, group, name)
		if err != nil {
			report(name, err)
			continue
		}
		releases = append(releases, release)
		update := NewUpdate(name, group, assetHash, options, run, streams)
		if err := update.CheckSandbox(ctx); err != nil {
			report(name, err)
			continue
		}
		if err := update.CheckOwner(ctx); err != nil {
			report(name, err)
			continue
		}
		if err := update.CheckInterruptedUpdate(); err != nil {
			report(name, err)
			continue
		}
		if !update.containerExists {
			if !update.hasVolumes() {
				report(name, update.checkContainerPresence())
				continue
			}
			if _, err := fmt.Fprintln(streams.Stdout, fmt.Sprintf(updateAllVolumesOnlyFormat, name)); err != nil {
				return err
			}
			continue
		}
		set, missing, err := update.plan(ctx)
		if err != nil {
			report(name, err)
			continue
		}
		if update.current {
			if err := update.Apply(ctx); err != nil {
				report(name, err)
			}
			continue
		}
		updates = append(updates, update)
		if missing {
			index, found := jobIndex[set.String()]
			if !found {
				index = len(jobs)
				jobIndex[set.String()] = index
				jobs = append(jobs, imageJob{set: set})
			}
			jobs[index].updates = append(jobs[index].updates, update)
		}
	}
	for _, job := range jobs {
		if err := job.updates[0].ensureImage(ctx, job.set); err != nil {
			return err
		}
		for _, update := range job.updates[1:] {
			update.image = job.updates[0].image
		}
	}
	for _, update := range updates {
		if err := update.CheckSessions(ctx); err != nil {
			report(update.name, err)
			continue
		}
		if err := update.Apply(ctx); err != nil {
			failed = true
			fmt.Fprintln(streams.Stderr, err)
		}
	}
	if failed {
		return errors.New(updateAllFailuresMessage)
	}
	return nil
}

func discoverGroupSandboxes(ctx context.Context, group string, run process.Runner) ([]string, error) {
	containers, err := queryContainers(ctx, run)
	if err != nil {
		return nil, err
	}
	volumes, err := queryVolumes(ctx, run)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	for _, container := range containers {
		for _, prefix := range []string{containerPrefix, backupPrefix} {
			if name, found := strings.CutPrefix(container.Names[0], prefix+group+"."); found {
				if err := ValidateName(name); err != nil {
					return nil, fmt.Errorf("invalid podman ps response: %w", err)
				}
				names[name] = true
			}
		}
	}
	for _, volume := range volumes {
		base, _, valid := splitSandboxVolume(volume.Name)
		if name, found := strings.CutPrefix(base, containerPrefix+group+"."); valid && found {
			if err := ValidateName(name); err != nil {
				return nil, fmt.Errorf("invalid podman volume ls response: %w", err)
			}
			names[name] = true
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	slices.Sort(result)
	return result, nil
}
