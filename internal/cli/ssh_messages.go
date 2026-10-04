package cli

const (
	sshMissingName      = "missing sandbox name; use sandboxed-agents ssh-config NAME [--install]"
	sshDuplicateInstall = "duplicate option \"--install\"; give it once"
	sshDuplicateFlag    = "duplicate option \"--ssh-config\"; give it once"
	sshRetryFormat      = "sandbox %[2]s is running, but its SSH setup was not installed: %[1]w; run sandboxed-agents ssh-config %[2]s --install to retry"
)
