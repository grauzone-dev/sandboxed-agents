package sandbox

const (
	sshNotInstalledFormat         = "The SSH setup for sandbox %[1]s is not installed; this host entry works only after sandboxed-agents ssh-config %[1]s --install\n"
	sshInstalledFormat            = "Installed the SSH setup for sandbox %[1]s; sandboxed-agents ssh-config %[1]s prints its host entry.\n"
	sshUnchangedFormat            = "The SSH setup for sandbox %s is already installed and its host key is unchanged; nothing changed.\n"
	sshIncompleteFormat           = "the SSH setup of sandbox %[1]s is incomplete: its host entry is missing from the managed SSH configuration; run sandboxed-agents ssh-config %[1]s --remove, then sandboxed-agents ssh-config %[1]s --install"
	sshHostKeyMismatchFormat      = "the host key of sandbox %[1]s differs from the pinned host key, and the pin was not changed; if you expect a new host key, run sandboxed-agents ssh-config %[1]s --remove, then sandboxed-agents ssh-config %[1]s --install"
	sshHostKeyFailureFormat       = "the manager did not report the sandbox's SSH host key: %s; nothing was changed. Restart the sandbox, which generates a missing host key, then retry the installation"
	sshQueryFailureFormat         = "ssh -G could not resolve your SSH configuration: %s; nothing was changed. Fix the reported error in your SSH configuration, then retry the installation"
	sshInvalidQuery               = "ssh -G printed no host and hostname; nothing was changed. Make sure the ssh on your PATH is OpenSSH, then retry the installation"
	sshNameConflictFormat         = "host entry %[1]s already resolves to a configured host: ssh -G -F with your SSH configuration differs from ssh -G -F none for %[1]s; nothing was changed. Remove or rename that host in your SSH configuration, or exclude it from a wildcard pattern such as Host * !%[1]s, then retry the installation"
	sshKeyGenerationFailureFormat = "ssh-keygen could not create the SSH key: %s; nothing was changed. Make sure OpenSSH is installed, then retry the installation"
	sshAuthorizationFailureFormat = "the manager could not authorize the SSH key: %s; your SSH configuration and host state were not changed. Restart the sandbox, then retry the installation"
	sshExistingStateFormat        = "host state path %s already exists without its host entry; nothing was changed. Move that path aside, then retry the installation"
)
