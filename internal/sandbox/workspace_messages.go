package sandbox

const (
	workspacePathError           = "workspace %q does not exist or cannot be read: %w"
	workspaceDirectoryError      = "workspace %q is not a directory"
	workspaceProtectedError      = "workspace %q contains or lies inside the protected host path %q"
	workspaceConflictError       = "workspace does not match the sandbox: recorded %q, given %q; omit WORKSPACE to start the sandbox as it is, or run sandboxed-agents remove %s, which keeps its volumes, and then sandboxed-agents up %s with the new WORKSPACE"
	workspaceUnusedVolumeMessage = "Kept volume %s unused: the workspace is a bind; remove --volumes deletes it."
	workspaceWindowsError        = "WORKSPACE is not supported on Windows yet; omit it to use a workspace volume"
	workspaceAliasError          = "cannot resolve %q: %w; up cannot rule out a protected host path"
)
