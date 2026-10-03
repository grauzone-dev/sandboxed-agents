package sandbox

const (
	limitsUnexpectedArgument = "unexpected argument"
	limitsUnknownOption      = "unknown option"
	limitsArgumentError      = "%s %q; up accepts only --memory, --cpus, --pids-limit, --shm-size, and --help after NAME"
	limitsDuplicateError     = "option %s is given more than once"
	limitsMissingValueError  = "missing value for option %s"
	limitsInvalidValueError  = "option %s does not accept %q; see sandboxed-agents up --help for the accepted values"
	limitsConflictError      = "resource limit %s does not match the sandbox: recorded %q, given %q (an empty recorded value means the container records no such limit); omit the option to start the sandbox as it is, or change the limit with sandboxed-agents remove %s, which keeps the sandbox's volumes, followed by sandboxed-agents up %s with the new value"
)
