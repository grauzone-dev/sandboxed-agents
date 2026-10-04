package sandbox

const (
	agentSessionNotEnabled = "agent %s is not enabled; run sandboxed-agents agents enable %s %s"
	agentSessionRunFailure = "could not run the agent session: %w"
	agentSessionFailure    = "agent session failed with exit status %d"
)
