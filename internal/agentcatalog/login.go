package agentcatalog

import (
	"fmt"
	"slices"
	"strings"
)

func (entry Entry) LoginWorkflow(workflow ...string) (string, []string, error) {
	if len(workflow) > 1 {
		return "", nil, fmt.Errorf(LoginWorkflowTooMany, entry.Name)
	}
	names := make([]string, 0, len(entry.LoginWorkflows))
	for workflowName := range entry.LoginWorkflows {
		names = append(names, workflowName)
	}
	slices.Sort(names)
	if len(names) == 0 {
		return "", nil, fmt.Errorf(LoginWorkflowUnavailable, entry.Name)
	}
	name := ""
	if len(workflow) == 0 {
		if len(names) != 1 {
			return "", nil, fmt.Errorf(LoginWorkflowRequired, entry.Name, strings.Join(names, ", "))
		}
		name = names[0]
	} else {
		name = workflow[0]
	}
	args, ok := entry.LoginWorkflows[name]
	if !ok {
		return "", nil, fmt.Errorf(LoginWorkflowUnknown, name, entry.Name, strings.Join(names, ", "))
	}
	return name, slices.Clone(args), nil
}
