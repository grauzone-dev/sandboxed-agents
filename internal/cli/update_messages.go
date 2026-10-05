package cli

const (
	updateMissingNameMessage = "missing update target; use sandboxed-agents update NAME [--with SET] or sandboxed-agents update --all"
	updateUsageMessage       = "update takes exactly one target, either NAME or --all; use sandboxed-agents update NAME [--with SET] or sandboxed-agents update --all"
	updateAllWithMessage     = "update --all does not take --with; change the toolchain set of one sandbox at a time with sandboxed-agents update NAME --with SET"
)
