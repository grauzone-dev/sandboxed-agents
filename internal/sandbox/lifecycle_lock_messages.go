package sandbox

const (
	lifecycleBusyFormat        = "another lifecycle command is in progress for sandbox %s; retry when it has finished"
	lifecycleLockObjectFormat  = "lifecycle lock %s is not a regular file; move it out of the way and retry"
	lifecycleLockFailureFormat = "cannot take the lifecycle lock of sandbox %s: %w"
)
