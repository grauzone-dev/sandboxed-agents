package cli

const (
	updateMissingNameMessage = "missing update target; use sandboxed-agents update NAME [--with SET] [--force] or sandboxed-agents update --all [--force]"
	updateUsageMessage       = "update takes exactly one target, either NAME or --all; use sandboxed-agents update NAME [--with SET] [--force] or sandboxed-agents update --all [--force]"
	updateAllWithMessage     = "update --all does not take --with; change the toolchain set of one sandbox at a time with sandboxed-agents update NAME --with SET"
)
