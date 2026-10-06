package manager

const (
	agentPinNone                      = "none"
	agentPinFormat                    = "Pin: %s.\n"
	agentEnabledVersionFormat         = "Agent %s is enabled (version %s).\n"
	agentUpdatedVersionFormat         = "Agent %s is updated (version %s).\n"
	agentVersionMissingMessage        = "option --version needs a version"
	agentVersionDuplicateMessage      = "option --version is given more than once"
	agentUnpinDuplicateMessage        = "option --unpin is given more than once"
	agentVersionInvalidFormat         = "invalid agent version %q; use an exact version such as 1.2.3"
	agentInstallVersionMismatchFormat = "installed version %s does not match requested version %s"
)
