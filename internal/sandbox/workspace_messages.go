package sandbox

const (
	workspacePathError           = "cannot use workspace %q: %w"
	workspaceDirectoryError      = "workspace %q is not a directory"
	workspaceProtectedError      = "workspace %q contains or lies inside the protected host path %q"
	workspaceConflictError       = "workspace does not match the sandbox: recorded %q, given %q; omit WORKSPACE to start the sandbox as it is, or run sandboxed-agents remove %s, which keeps its volumes, and then sandboxed-agents up %s with the new WORKSPACE"
	workspaceUnusedVolumeMessage = "Kept volume %s unused: the workspace is a bind; remove --volumes deletes it."
	workspaceUnsupportedError    = "WORKSPACE is not supported on %s"
	workspaceAliasError          = "cannot resolve %q: %w; up cannot rule out a protected host path"
	workspaceAutomountError      = "cannot translate WORKSPACE: the Podman machine reports the WSL automount root %q, and up supports only the default root /mnt/; an empty root means that automount is disabled or its root could not be read, which is refused as well; omit WORKSPACE to use a workspace volume"
	workspaceWindowsPathError    = "workspace %q does not start with a drive letter; up translates only drive-letter paths into the Podman machine, not UNC or other non-drive paths"
)
