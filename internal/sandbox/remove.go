package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type Remove struct {
	*sandboxObjects
	deleteVolumes bool
	force         bool
	sessions      []manager.Session
	sessionsKnown bool
}

func NewRemove(name string, deleteVolumes, force bool, run process.Runner, streams process.Streams) *Remove {
	return &Remove{sandboxObjects: newSandboxObjects(name, run, streams), deleteVolumes: deleteVolumes, force: force}
}

func (remove *Remove) CheckSandbox(ctx context.Context) error {
	if err := remove.sandboxObjects.CheckSandbox(ctx); err != nil {
		return err
	}
	if remove.containerExists {
		return nil
	}
	for _, volume := range remove.volumes {
		if volume.exists {
			return nil
		}
	}
	return fmt.Errorf("no sandbox named %s exists: neither its container nor any of its volumes was found", remove.name)
}

func (remove *Remove) CheckOwner(ctx context.Context) error {
	if !remove.containerExists {
		return remove.sandboxObjects.CheckOwner(ctx)
	}
	if err := remove.inspectBackup(ctx); err != nil {
		return err
	}
	conflicts := remove.ownerConflicts()
	if remove.backupExists && !isOwned(remove.backupOwner) {
		conflicts = append(conflicts, remove.backup)
	}
	if len(conflicts) == 0 || (remove.deleteVolumes && isOwned(remove.containerOwner) && !remove.backupExists) {
		return nil
	}
	return ownerConflict(conflicts)
}

func (remove *Remove) Apply(ctx context.Context) error {
	if remove.containerExists {
		if remove.containerRunning {
			if err := remove.runPodman(ctx, "stop", remove.container); err != nil {
				return err
			}
			if err := remove.reportEndedSessions(); err != nil {
				return err
			}
		}
		if err := remove.runPodman(ctx, "rm", remove.container); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(remove.streams.Stdout, "Removed container %s.\n", remove.container); err != nil {
			return err
		}
	} else if !remove.deleteVolumes {
		_, err := fmt.Fprintf(remove.streams.Stdout, "sandbox %s has no container; its volumes were kept, and sandboxed-agents remove %s --volumes deletes them\n", remove.name, remove.name)
		return err
	}
	foreign := false
	for _, volume := range remove.volumes {
		if !volume.exists {
			continue
		}
		message := "Kept volume %s.\n"
		if remove.deleteVolumes {
			if !isOwned(volume.owner) {
				message = "Kept volume %s: its owner label is missing or names another controller group; remove or rename it with Podman.\n"
				foreign = true
			} else {
				if err := remove.runPodman(ctx, "volume", "rm", volume.name); err != nil {
					return err
				}
				message = "Removed volume %s.\n"
			}
		}
		if _, err := fmt.Fprintf(remove.streams.Stdout, message, volume.name); err != nil {
			return err
		}
	}
	if foreign {
		return errors.New("volumes with a missing or different owner were kept; the container and the owned volumes were removed")
	}
	return nil
}

func (remove *Remove) CheckManager(ctx context.Context) error {
	if !remove.containerRunning {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var err error
	remove.sessions, err = RunningSessions(ctx, remove.container, remove.run)
	remove.sessionsKnown = err == nil
	if !remove.sessionsKnown && !remove.force {
		return errors.New("the sandbox manager did not answer, so running agent sessions cannot be ruled out; nothing was removed; use --force to remove the sandbox anyway")
	}
	return nil
}

func (remove *Remove) CheckSessions() error {
	if len(remove.sessions) > 0 && !remove.force {
		return fmt.Errorf("agent sessions are running: %s; nothing was removed; use --force to end them and remove the sandbox", remove.sessionNames())
	}
	return nil
}

func (remove *Remove) sessionNames() string {
	names := make([]string, len(remove.sessions))
	for index, session := range remove.sessions {
		names[index] = session.Agent + "/" + session.Name
	}
	return strings.Join(names, ", ")
}

func (remove *Remove) reportEndedSessions() error {
	if !remove.sessionsKnown {
		_, err := fmt.Fprint(remove.streams.Stdout, "The sandbox manager did not answer; agent sessions that may have been running were ended and cannot be named.\n")
		return err
	}
	if len(remove.sessions) > 0 {
		_, err := fmt.Fprintf(remove.streams.Stdout, "Ended agent sessions: %s.\n", remove.sessionNames())
		return err
	}
	return nil
}
