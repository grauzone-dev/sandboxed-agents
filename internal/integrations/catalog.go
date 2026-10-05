package integrations

import (
	"fmt"
	"slices"
	"strings"
)

type Integration struct {
	Name   string
	Login  []string
	Config []string
}

func Catalog() []Integration {
	return []Integration{
		{Name: "git", Config: []string{"identity", "credentials"}},
		{Name: "github", Login: []string{"device"}},
		{Name: "azure", Login: []string{"device"}},
		{Name: "azdo"},
	}
}

func (integration Integration) Workflows(kind string) []string {
	if kind == "login" {
		return integration.Login
	}
	return integration.Config
}

func Resolve(catalog []Integration, kind, name, workflow string) (string, error) {
	if kind != "config" && kind != "login" {
		return "", fmt.Errorf(UnknownKind, kind)
	}
	var valid []string
	for _, integration := range catalog {
		if len(integration.Workflows(kind)) > 0 {
			valid = append(valid, integration.Name)
		}
	}
	for _, integration := range catalog {
		if integration.Name != name {
			continue
		}
		workflows := integration.Workflows(kind)
		if name == "git" && kind == "login" && len(workflows) == 0 {
			return "", fmt.Errorf(GitHasNoLogin, strings.Join(integration.Config, ", "))
		}
		if len(workflows) == 0 {
			break
		}
		if workflow == "" {
			if len(workflows) == 1 {
				return workflows[0], nil
			}
			return "", fmt.Errorf(MissingWorkflow, kind, name, strings.Join(workflows, ", "))
		}
		if !slices.Contains(workflows, workflow) {
			return "", fmt.Errorf(UnknownWorkflow, kind, workflow, name, strings.Join(workflows, ", "))
		}
		return workflow, nil
	}
	if len(valid) == 0 {
		return "", fmt.Errorf(NoIntegrations, name, kind)
	}
	return "", fmt.Errorf(UnknownIntegration, name, kind, strings.Join(valid, ", "))
}
