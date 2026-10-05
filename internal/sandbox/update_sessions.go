package sandbox

import (
	"context"
	"fmt"
)

func (update *Update) CheckSessions(ctx context.Context) error {
	if update.backupExists || update.current || !update.containerRunning {
		return nil
	}
	var err error
	update.sessions, err = RunningSessions(ctx, update.container, update.run)
	update.sessionsKnown = err == nil
	if err != nil && !update.options.Force {
		return fmt.Errorf(updateSessionsUnknownFormat, update.name, err)
	}
	if len(update.sessions) > 0 && !update.options.Force {
		return fmt.Errorf(updateSessionsRunningFormat, update.name, formatSessionNames(update.sessions))
	}
	return nil
}

func (update *Update) reportEndedSessions(confirmed bool) error {
	message := ""
	if !update.sessionsKnown {
		format := updateSessionsUnknownMayEndedFormat
		if confirmed {
			format = updateSessionsUnknownEndedFormat
		}
		message = fmt.Sprintf(format, update.name)
	} else if len(update.sessions) > 0 {
		format := updateSessionsMayEndedFormat
		if confirmed {
			format = updateSessionsEndedFormat
		}
		message = fmt.Sprintf(format, update.name, formatSessionNames(update.sessions))
	}
	if message == "" {
		return nil
	}
	_, err := fmt.Fprintln(update.streams.Stdout, message)
	return err
}
