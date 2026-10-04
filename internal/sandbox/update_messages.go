package sandbox

const (
	updateAlreadyCurrentFormat       = "Sandbox %s is already up to date."
	updateSuccessFormat              = "Sandbox %s is updated."
	updateNoContainerFormat          = "sandbox %[1]s has no container; run sandboxed-agents up %[1]s, which adopts its volumes"
	updateMissingImageIDFormat       = "container %s reports no image ID, so update cannot tell whether it is outdated; inspect it with Podman"
	updateMissingVolumeFormat        = "%s does not exist; update replaces a container only when the volumes it mounts exist"
	updateInvalidMountsFormat        = "container %s does not mount the sandbox's volumes as up creates them, so update cannot recreate it; inspect it with Podman"
	updateInvalidConfigurationFormat = "container %s records no valid %s, so update cannot recreate it with the same configuration; inspect it with Podman"
	updateInterruptedFormat          = "backup container %[1]s remains from an interrupted update of sandbox %[2]s; recovering an interrupted update is not available in this version, so update changes nothing; inspect %[1]s with Podman"
	updateImageChangedFormat         = "image %[1]s changed while update prepared it, and sandbox %[2]s was not changed; retry sandboxed-agents update %[2]s"
)
