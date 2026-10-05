package sandbox

const (
	sshRemovedFormat                    = "Removed the SSH setup for sandbox %s from this host.\n"
	sshNotPresentFormat                 = "Sandbox %s has no SSH setup; nothing changed.\n"
	sshAuthorizationRemainsFormat       = "Sandbox %[1]s is not running, so its SSH key remains authorized in the sandbox; the next sandboxed-agents ssh-config %[1]s --install replaces that authorization.\n"
	sshVolumeAuthorizationRemainsFormat = "The SSH key of sandbox %[1]s remains authorized in its kept SSH server state volume; the next sandboxed-agents ssh-config %[1]s --install replaces that authorization.\n"
	sshDeauthorizationFailureFormat     = "removed the SSH setup for sandbox %[1]s from this host, but the manager could not remove the authorization of its SSH key in the sandbox: %[2]w; run sandboxed-agents check %[1]s for diagnosis, then sandboxed-agents restart %[1]s; the next sandboxed-agents ssh-config %[1]s --install replaces that authorization"
	sshDeauthorizationStatusFormat      = "ssh deauthorize exited with a non-zero status: %s"
)
