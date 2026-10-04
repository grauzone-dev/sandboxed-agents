package platform

import (
	"os"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func HasInteractiveTerminal(streams process.Streams) bool {
	stdout, ok := streams.Stdout.(*os.File)
	return IsTerminal(streams.Stdin) && ok && IsTerminal(stdout)
}
