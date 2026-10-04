package manager

const (
	agentSessionUsage              = "usage: sandboxed-agents-manager agents session NAME AGENT [--stop]"
	agentSessionIdentity           = "agents session must run as UID and GID 1000"
	agentSessionNeedsTerminal      = "agents session needs an interactive terminal to start or attach to a session; use --stop to end a session without one"
	agentSessionNotRunningFormat   = "No agent session of %s is running; nothing to do.\n"
	agentSessionStoppedFormat      = "Ended the agent session of %s.\n"
	agentSessionStartFailure       = "could not start the agent session: %w"
	agentSessionStartStatusFormat  = "starting the agent session failed with exit status %d"
	agentSessionAttachFailure      = "could not attach to the agent session: %w"
	agentSessionAttachStatusFormat = "attaching to the agent session failed with exit status %d"
	agentSessionStopFailure        = "could not end the agent session: %w"
	agentSessionStopStatusFormat   = "ending the agent session failed with exit status %d"
	agentSessionWorkerUsage        = "usage: sandboxed-agents-manager agents session-worker NAME AGENT"
)
