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
		{Name: "git", Config: []string{"identity"}},
		{Name: "github"},
		{Name: "azure"},
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

type Request struct {
	Kind        string
	Integration string
	Workflow    string
	Name        *string
	Email       *string
}

func Parse(kind string, args []string) (Request, error) {
	request := Request{Kind: kind}
	if len(args) == 0 {
		return request, fmt.Errorf(MissingIntegration, kind)
	}
	request.Integration = args[0]
	args = args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		request.Workflow = args[0]
		args = args[1:]
	}
	workflow, err := Resolve(Catalog(), kind, request.Integration, request.Workflow)
	if err != nil {
		return request, err
	}
	request.Workflow = workflow
	for len(args) > 0 {
		option, value, inline := strings.Cut(args[0], "=")
		var target **string
		switch option {
		case "--name":
			target = &request.Name
		case "--email":
			target = &request.Email
		default:
			return request, fmt.Errorf(UnexpectedArgument, args[0])
		}
		if *target != nil {
			return request, fmt.Errorf(DuplicateOption, option)
		}
		args = args[1:]
		if !inline {
			if len(args) == 0 || strings.HasPrefix(args[0], "--") {
				return request, fmt.Errorf(MissingOptionValue, option)
			}
			value, args = args[0], args[1:]
		}
		if strings.ContainsRune(value, 0) {
			return request, fmt.Errorf(InvalidOptionValue, option)
		}
		*target = &value
	}
	return request, nil
}

func (request Request) NeedsTerminal() bool {
	return request.Name == nil || request.Email == nil
}

func (request Request) Args() []string {
	args := []string{"integrations", request.Kind, request.Integration, request.Workflow}
	if request.Name != nil {
		args = append(args, "--name="+*request.Name)
	}
	if request.Email != nil {
		args = append(args, "--email="+*request.Email)
	}
	return args
}
