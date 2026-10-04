package manager

const (
	agentLoginUsage        = "usage: sandboxed-agents-manager agents login AGENT [WORKFLOW]"
	agentLoginCheckUsage   = "usage: sandboxed-agents-manager agents check-enabled AGENT"
	agentLoginIdentity     = "agents login and agents check-enabled must run as UID and GID 1000"
	agentLoginNotEnabled   = "agent %s is not enabled; run sandboxed-agents agents enable %s %s"
	agentLoginStartFailure = "could not start the login workflow of %s: %w"
	agentLoginFailure      = "login workflow of %s failed with exit status %d"
)
