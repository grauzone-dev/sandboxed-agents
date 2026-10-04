package sandbox

const (
	fingerprintReadFailedFormat   = "read SSH host key %s: %w"
	fingerprintPodmanFailedFormat = "read SSH host key %s: podman exec failed with exit status %d: %s"
	fingerprintInvalidKeyFormat   = "invalid SSH host public key %s"
)
