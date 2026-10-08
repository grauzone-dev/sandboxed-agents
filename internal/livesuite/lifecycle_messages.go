package livesuite

const (
	localPodmanRequiredMessage       = "the lifecycle part needs a local Podman service on Linux: unset CONTAINER_HOST and CONTAINER_CONNECTION and use a Podman that is not remote"
	changedPodmanTargetMessage       = "the selected Podman machine changed during the lifecycle part: the command was refused so that it cannot act on another machine; check the suite's controller group for leftovers"
	groupNotEmptyMessage             = "the lifecycle part needs an empty dedicated controller group: Podman reports a container or volume owned by the group or named with its prefix; choose another group or remove those objects"
	invalidContainerInventoryMessage = "Podman returned a container inventory that the lifecycle part cannot read"
	invalidVolumeInventoryMessage    = "Podman returned a volume inventory that the lifecycle part cannot use: it is unreadable, or the sandbox does not have exactly its three named volumes owned by the controller group"
	invalidLifecycleListMessage      = "sandboxed-agents list returned output that the lifecycle part cannot read"
	unexpectedLifecycleStateMessage  = "sandboxed-agents list did not report the lifecycle sandbox in the expected state"
	changedLifecycleVolumesMessage   = "the volumes of the lifecycle sandbox changed: remove without --volumes or the adopting up did not keep them"
)
