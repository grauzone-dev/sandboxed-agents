package cli

const (
	sshMissingName         = "missing sandbox name; use sandboxed-agents ssh-config NAME [--install|--remove]"
	sshConflictingMode     = "conflicting or repeated options; give at most one of --install and --remove, once"
	sshDuplicateFlag       = "duplicate option \"--ssh-config\"; give it once"
	sshDeferredRetryFormat = "%[1]w; the SSH setup of sandbox %[2]s was not installed; once the sandbox runs, run sandboxed-agents ssh-config %[2]s --install to retry"
	sshRetryFormat         = "sandbox %[2]s is running, but its SSH setup was not installed: %[1]w; run sandboxed-agents ssh-config %[2]s --install to retry"
)
