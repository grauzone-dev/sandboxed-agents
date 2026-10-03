package sandbox

import (
	"context"
	"fmt"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type LifecycleAction string

const (
	Start   LifecycleAction = "start"
	Stop    LifecycleAction = "stop"
	Restart LifecycleAction = "restart"
)

type Lifecycle struct {
	*sandboxObjects
	stops           bool
	starts          bool
	force           bool
	sessions        []manager.Session
	sessionsUnknown bool
}

func NewLifecycle(name string, action LifecycleAction, force bool, run process.Runner, streams process.Streams) *Lifecycle {
	return &Lifecycle{sandboxObjects: newSandboxObjects(name, run, streams), stops: action != Start, starts: action != Stop, force: force}
}

func (lifecycle *Lifecycle) CheckSandbox(ctx context.Context) error {
	if err := lifecycle.sandboxObjects.CheckSandbox(ctx); err != nil {
		return err
	}
	if lifecycle.containerExists {
		return nil
	}
	for _, volume := range lifecycle.volumes {
		if volume.exists {
			return fmt.Errorf("sandbox %[1]s has no container; run sandboxed-agents up %[1]s, which adopts its volumes", lifecycle.name)
		}
	}
	return fmt.Errorf("sandbox %s does not exist in this controller group", lifecycle.name)
}

func (lifecycle *Lifecycle) start(ctx context.Context) error {
	if !lifecycle.containerRunning {
		if err := lifecycle.runPodman(ctx, "start", lifecycle.container); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(lifecycle.streams.Stdout, "Sandbox %s is running.\n", lifecycle.name)
	return err
}

func (lifecycle *Lifecycle) CheckSessions(ctx context.Context) error {
	if !lifecycle.stops || !lifecycle.containerRunning {
		return nil
	}
	sessions, err := RunningSessions(ctx, lifecycle.container, lifecycle.run)
	if err != nil {
		if !lifecycle.force {
			return fmt.Errorf("cannot rule out running agent sessions in sandbox %s: %s; use --force to proceed anyway", lifecycle.name, err)
		}
		lifecycle.sessionsUnknown = true
		return nil
	}
	lifecycle.sessions = sessions
	return nil
}

func (lifecycle *Lifecycle) Apply(ctx context.Context) error {
	if lifecycle.stops && lifecycle.containerRunning {
		if err := lifecycle.runPodman(ctx, "stop", lifecycle.container); err != nil {
			return err
		}
		lifecycle.containerRunning = false
		if lifecycle.sessionsUnknown {
			if _, err := fmt.Fprint(lifecycle.streams.Stdout, "Agent sessions that may have been running were ended and cannot be named.\n"); err != nil {
				return err
			}
		}
		if len(lifecycle.sessions) > 0 {
			if _, err := fmt.Fprintf(lifecycle.streams.Stdout, "Ended agent sessions: %s.\n", lifecycle.sessionNames()); err != nil {
				return err
			}
		}
	}
	if lifecycle.starts {
		return lifecycle.start(ctx)
	}
	_, err := fmt.Fprintf(lifecycle.streams.Stdout, "Sandbox %s is stopped.\n", lifecycle.name)
	return err
}

func (lifecycle *Lifecycle) sessionNames() string {
	names := make([]string, len(lifecycle.sessions))
	for index, session := range lifecycle.sessions {
		names[index] = session.Name + " (" + session.Agent + ")"
	}
	return strings.Join(names, ", ")
}

func (lifecycle *Lifecycle) CheckSessionGuard() error {
	if len(lifecycle.sessions) > 0 && !lifecycle.force {
		return fmt.Errorf("sandbox %s has running agent sessions: %s; use --force to end them", lifecycle.name, lifecycle.sessionNames())
	}
	return nil
}
