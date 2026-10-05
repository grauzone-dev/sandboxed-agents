package sandbox

const (
	updateSessionsUnknownFormat      = "cannot rule out running agent sessions in sandbox %s: %w; update changed nothing in the sandbox; use --force to update it anyway"
	updateSessionsRunningFormat      = "sandbox %s has running agent sessions: %s; update changed nothing in the sandbox; use --force to end them and update it"
	updateSessionsEndedFormat        = "Stopping the old container of sandbox %s ended these agent sessions: %s."
	updateSessionsUnknownEndedFormat = "Stopping the old container of sandbox %s ended agent sessions that may have been running; they cannot be named because the manager did not answer."
)
