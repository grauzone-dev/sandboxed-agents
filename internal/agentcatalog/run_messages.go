package agentcatalog

const (
	RunMissingSandbox      = "missing sandbox name; use sandboxed-agents agents run NAME AGENT"
	RunMissingAgent        = "missing agent name; use sandboxed-agents agents run NAME AGENT"
	RunUnknownAgentFormat  = "unknown agent %q; valid agents: %s"
	RunNoAgentsFormat      = "unknown agent %q; no valid agent names are available yet"
	RunManagerUsage        = "usage: sandboxed-agents-manager agents run NAME AGENT [ARG...]"
	RunIdentity            = "agents run must run as UID and GID 1000"
	RunSelectionInvalid    = "agent selection is not a valid JSON object"
	RunSelectionReadFormat = "read agent selection: %w"
	RunNotEnabledFormat    = "agent %s is not enabled; use sandboxed-agents agents enable %s %s"
	RunStartFormat         = "start agent %s: %w"
)
