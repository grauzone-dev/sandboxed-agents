package manager

const (
	agentSessionQueryFailure      = "could not query the agent sessions: %w"
	agentSessionQueryStatusFormat = "agent session query failed with exit status %d: %s"
	agentSessionInvalidList       = "agent session query returned an invalid session list"
	sessionQueryIdentity          = "sessions list must run as root or as UID and GID 1000"
)
