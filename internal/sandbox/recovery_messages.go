package sandbox

const (
	recoveryCompletedFormat    = "The interrupted update of sandbox %s was completed, and the sandbox is updated."
	recoveryRestoredFormat     = "an interrupted update of sandbox %s was restored: its original container is back under its name, and update made no further change, so the sandbox was not updated; processes that ended during the interrupted update were not restarted"
	recoveryUnknownStateFormat = "the previous state could not be determined, so sandbox %[1]s stays stopped; if it was running, start it with sandboxed-agents start %[1]s"
	recoveryInvalidStateFormat = "container %s records no valid %s label, so update cannot recover the interrupted update safely and changed no container; inspect the sandbox's containers with Podman"
	recoveryInvalidPortFormat  = "container %s records no valid ssh-port label, so the readiness of the interrupted update could not be checked"
	recoveryStepFailureFormat  = "recovery of the interrupted update of sandbox %s failed at step %q: %w; update stopped the recovery so that two containers never run on the same volumes; the original container is kept, under the backup name unless it was already renamed back, and while the backup container remains, the sandbox counts as \"update interrupted\"; inspect the sandbox's containers with Podman"
)
