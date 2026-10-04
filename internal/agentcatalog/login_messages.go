package agentcatalog

const (
	LoginWorkflowRequired    = "workflow name required for agent %q; valid workflows: %s"
	LoginWorkflowUnknown     = "unknown workflow %q for agent %q; valid workflows: %s"
	LoginWorkflowUnavailable = "agent %q has no login workflow; no valid workflow names are available yet"
	LoginWorkflowTooMany     = "more than one workflow name for agent %q; name at most one"
)
