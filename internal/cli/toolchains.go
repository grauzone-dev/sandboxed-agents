package cli

import (
	"fmt"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

func parseToolchains(args []string) (toolchains.Set, bool, []string, error) {
	var selection toolchains.Set
	var provided bool
	var remaining []string
	for index := 0; index < len(args); index++ {
		option, value, inline := strings.Cut(args[index], "=")
		if option != "--with" {
			remaining = append(remaining, args[index])
			continue
		}
		if provided {
			return selection, false, nil, fmt.Errorf("duplicate option %q", option)
		}
		provided = true
		if !inline {
			index++
			if index == len(args) {
				return selection, false, nil, fmt.Errorf("missing value for %s; valid values: %s", option, strings.Join(toolchains.ValidValues(), ", "))
			}
			value = args[index]
		}
		var err error
		selection, err = toolchains.Parse(value)
		if err != nil {
			return selection, false, nil, err
		}
	}
	return selection, provided, remaining, nil
}
