package sandbox

const (
	readinessWaitFailureFormat = "readiness wait for sandbox container %s failed: %w"
	readinessManagerError      = "the manager does not answer its version query"
	readinessSSHError          = "the SSH server does not complete an SSH key exchange on its loopback port"
)
