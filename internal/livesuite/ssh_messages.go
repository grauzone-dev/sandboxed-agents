package livesuite

const (
	sshStandardAccountMessage = "the SSH live part requires a Windows standard account without administrator membership"
	sshClientMessage          = "the SSH live part requires host OpenSSH (OpenSSH for Windows on Windows)"
	sshStateOccupiedMessage   = "the SSH live part requires a controller group with no existing SSH setup"
	sshBindingMessage         = "the SSH sandbox must publish port 22 only on 127.0.0.1"
	sshConfigMessage          = "host OpenSSH did not resolve the sandbox's dedicated key, pinned host key, and restricted host entry"
	sshIdentityMessage        = "the SSH connection did not succeed as agent without diagnostics"
	sshStateChangedMessage    = "the SSH setup changed during stop and start"
	sshCleanupMessage         = "the SSH live part left host SSH files or changed the user's SSH configuration"
)
