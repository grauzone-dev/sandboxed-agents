package sandbox

const (
	updateAllSkippedFormat     = "sandbox %s was passed over and not updated: %w"
	updateAllVolumesOnlyFormat = "Sandbox %[1]s was passed over because only its volumes remain; sandboxed-agents up %[1]s adopts them."
	updateAllFailuresMessage   = "update --all could not update one or more sandboxes; each sandbox that failed or was not updated is reported in its own message"
)
