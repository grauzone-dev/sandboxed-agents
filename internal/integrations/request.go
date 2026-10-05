package integrations

import (
	"errors"
	"fmt"
	"strings"
)

type Request struct {
	Kind        string
	Integration string
	Workflow    string
	CommitName  *string
	CommitEmail *string
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
		// The azdo workflow word may be a mistyped token, and the generic error quotes it.
		// Return the fixed message so no typed value is ever echoed.
		if kind == "login" && request.Integration == "azdo" {
			return request, errors.New(AzdoUnknownWorkflow)
		}
		return request, err
	}
	request.Workflow = workflow
	for len(args) > 0 {
		// Never quote an extra azdo argument: it may be a token passed by mistake.
		if kind == "login" && request.Integration == "azdo" {
			return request, errors.New(AzdoUnexpectedArgument)
		}
		if request.Workflow != "identity" {
			return request, fmt.Errorf(UnexpectedArgument, args[0])
		}
		option, value, inline := strings.Cut(args[0], "=")
		var target **string
		switch option {
		case "--name":
			target = &request.CommitName
		case "--email":
			target = &request.CommitEmail
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
		if option == "--name" && value == "" {
			return request, fmt.Errorf(MissingOptionValue, option)
		}
		*target = &value
	}
	return request, nil
}

func (request Request) NeedsTerminal() bool {
	return request.Kind == "login" && request.Integration != "azdo" ||
		request.Workflow == "identity" && (request.CommitName == nil || request.CommitEmail == nil)
}

func (request Request) Args() []string {
	args := []string{"integrations", request.Kind, request.Integration, request.Workflow}
	if request.CommitName != nil {
		args = append(args, "--name="+*request.CommitName)
	}
	if request.CommitEmail != nil {
		args = append(args, "--email="+*request.CommitEmail)
	}
	return args
}
