package livesuite

const (
	localPodmanRequiredMessage       = "the live suite needs a local Podman service on Linux: unset CONTAINER_HOST and CONTAINER_CONNECTION and use a Podman that is not remote"
	changedPodmanTargetMessage       = "the selected Podman machine changed during the live suite run: the command was refused so that it cannot act on another machine; check the suite's controller group for leftovers"
	groupNotEmptyMessage             = "the live suite's dedicated controller group is not empty: Podman reports a container or volume owned by the group or named with its prefix; choose another group or remove those objects"
	invalidContainerInventoryMessage = "Podman returned a container inventory that the live suite cannot read"
	invalidVolumeInventoryMessage    = "Podman returned a volume inventory that the live suite cannot use: it is unreadable, or a sandbox of the suite does not have exactly its three named volumes owned by the controller group"
	invalidLifecycleListMessage      = "sandboxed-agents list returned output that the lifecycle part cannot read"
	unexpectedLifecycleStateMessage  = "sandboxed-agents list did not report the lifecycle sandbox in the expected state"
	changedLifecycleVolumesMessage   = "the volumes of the lifecycle sandbox changed: remove without --volumes or the adopting up did not keep them"
)
