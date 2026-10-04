package sandbox

const (
	agentLoginNotEnabled      = "agent %s is not enabled; run sandboxed-agents agents enable %s %s"
	agentLoginRunFailure      = "could not run the login workflow: %w"
	agentLoginWorkflowFailure = "login workflow failed with exit status %d"
)
