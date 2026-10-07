package manager

const (
	agentUsageMessage    = "usage: sandboxed-agents-manager agents enable AGENT [--version X] [--force], sandboxed-agents-manager agents update NAME AGENT [--unpin] [--force], sandboxed-agents-manager agents disable AGENT [--force],sandboxed-agents-manager agents status AGENT, sandboxed-agents-manager agents list, sandboxed-agents-manager agents login AGENT [WORKFLOW], sandboxed-agents-manager agents session NAME AGENT [--stop], or sandboxed-agents-manager agents check-enabled AGENT"
	agentIdentityMessage = "agents enable, update, disable, status, and list must run as root or as UID and GID 1000"
)
