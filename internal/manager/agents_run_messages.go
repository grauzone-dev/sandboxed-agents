package manager

const (
	agentRunUsage            = "usage: sandboxed-agents-manager agents run NAME AGENT [ARG...]"
	agentRunIdentity         = "agents run must run as UID and GID 1000"
	agentSelectionInvalid    = "agent selection is not a valid JSON object"
	agentSelectionReadFormat = "read agent selection: %w"
	agentNotEnabledFormat    = "agent %s is not enabled; use sandboxed-agents agents enable %s %s"
	agentRunStartFormat      = "start agent %s: %w"
	agentUnknownFormat       = "unknown agent %q; valid agents: %s"
)
