package sandbox

import (
	"context"
	"errors"
	"fmt"
)

func (update *Update) RecoverInterruptedUpdate(ctx context.Context) error {
	if !update.backupExists {
		return nil
	}
	wasRunning := update.backupRunning
	if update.containerExists {
		switch update.containerLabels[UpdateWasRunningLabel] {
		case "true":
			wasRunning = true
		case "false":
			wasRunning = false
		default:
			return fmt.Errorf(recoveryInvalidStateFormat, update.container, UpdateWasRunningLabel)
		}
	}
	if !update.containerExists || update.backupRunning {
		return update.restoreInterrupted(ctx, wasRunning, nil)
	}
	port, err := parseSSHPort(update.containerLabels[SSHPortLabel])
	if err != nil {
		return update.restoreInterrupted(ctx, wasRunning, fmt.Errorf(recoveryInvalidPortFormat, update.container))
	}
	if !update.containerRunning {
		if err := update.runPodman(ctx, "start", update.container); err != nil {
			return update.restoreInterrupted(ctx, wasRunning, err)
		}
	}
	if err := WaitReady(ctx, update.container, port, update.run); err != nil {
		return update.restoreInterrupted(ctx, wasRunning, err)
	}
	if err := update.finishReplacement(ctx, wasRunning); err != nil {
		return err
	}
	update.recovered = true
	_, err = fmt.Fprintln(update.streams.Stdout, fmt.Sprintf(recoveryCompletedFormat, update.name))
	return err
}

func (update *Update) restoreInterrupted(ctx context.Context, wasRunning bool, cause error) error {
	if step, err := update.restoreBackup(ctx, update.containerExists, wasRunning && !update.backupRunning); err != nil {
		return errors.Join(cause, fmt.Errorf(recoveryStepFailureFormat, update.name, step, err))
	}
	failure := fmt.Errorf(recoveryRestoredFormat, update.name)
	if !update.containerExists && !update.backupRunning {
		failure = fmt.Errorf("%w; %s", failure, fmt.Sprintf(recoveryUnknownStateFormat, update.name))
	}
	return errors.Join(cause, failure)
}
