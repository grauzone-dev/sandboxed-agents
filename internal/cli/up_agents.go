package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
)

func parseUpAgents(args []string, catalog agentcatalog.Catalog) ([]string, []string, error) {
	var agents, remaining []string
	provided := false
	for index := 0; index < len(args); index++ {
		option, value, inline := strings.Cut(args[index], "=")
		if option != "--agents" {
			remaining = append(remaining, args[index])
			continue
		}
		if provided {
			return nil, nil, errors.New(upAgentsDuplicateOptionMessage)
		}
		provided = true
		if !inline {
			index++
			if index == len(args) || strings.HasPrefix(args[index], "--") {
				return nil, nil, errors.New(upAgentsMissingValueMessage)
			}
			value = args[index]
		}
		for _, name := range strings.Split(value, ",") {
			if name == "" {
				return nil, nil, errors.New(upAgentsEmptyNameMessage)
			}
			if err := validateAgent(name, catalog); err != nil {
				return nil, nil, err
			}
			if !slices.Contains(agents, name) {
				agents = append(agents, name)
			}
		}
	}
	return agents, remaining, nil
}

func validateAgent(name string, catalog agentcatalog.Catalog) error {
	if _, ok := catalog.Find(name); ok {
		return nil
	}
	names := catalog.Names()
	if len(names) == 0 {
		return fmt.Errorf(agentNoneFormat, name)
	}
	return fmt.Errorf(agentUnknownFormat, name, strings.Join(names, ", "))
}
