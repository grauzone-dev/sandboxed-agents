package sandbox

import (
	"context"
	"fmt"
	"strings"
)

func (update *Update) CheckSessions(ctx context.Context) error {
	if update.current || !update.containerRunning {
		return nil
	}
	var err error
	update.sessions, err = RunningSessions(ctx, update.container, update.run)
	update.sessionsKnown = err == nil
	if err != nil && !update.options.Force {
		return fmt.Errorf(updateSessionsUnknownFormat, update.name, err)
	}
	if len(update.sessions) > 0 && !update.options.Force {
		return fmt.Errorf(updateSessionsRunningFormat, update.name, update.sessionNames())
	}
	return nil
}

func (update *Update) sessionNames() string {
	names := make([]string, len(update.sessions))
	for index, session := range update.sessions {
		names[index] = session.Name + " (" + session.Agent + ")"
	}
	return strings.Join(names, ", ")
}

func (update *Update) reportEndedSessions() error {
	message := ""
	if !update.sessionsKnown {
		message = fmt.Sprintf(updateSessionsUnknownEndedFormat, update.name)
	} else if len(update.sessions) > 0 {
		message = fmt.Sprintf(updateSessionsEndedFormat, update.name, update.sessionNames())
	}
	if message == "" {
		return nil
	}
	_, err := fmt.Fprintln(update.streams.Stdout, message)
	return err
}
