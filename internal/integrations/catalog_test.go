package integrations_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/integrations"
)

func TestWorkflowNameIsOptionalOnlyForOneWorkflowOfTheRequestedKind(t *testing.T) {
	catalog := []integrations.Integration{{Name: "git", Config: []string{"identity", "credentials"}}, {Name: "github", Login: []string{"device"}}}
	workflow, err := integrations.Resolve(catalog, "config", "git", "")
	if err == nil || workflow != "" || !strings.Contains(err.Error(), "identity, credentials") {
		t.Fatalf("workflow=%q err=%v", workflow, err)
	}
	for _, test := range []struct{ kind, name, workflow, want string }{
		{"config", "git", "identity", "identity"},
		{"config", "git", "credentials", "credentials"},
		{"login", "github", "", "device"},
	} {
		workflow, err := integrations.Resolve(catalog, test.kind, test.name, test.workflow)
		if err != nil || workflow != test.want {
			t.Fatalf("workflow=%q err=%v", workflow, err)
		}
	}
}

func TestIntegrationCatalogContainsOnlyDeliveredWorkflows(t *testing.T) {
	want := []integrations.Integration{{Name: "git", Config: []string{"identity", "credentials"}}, {Name: "github", Login: []string{"device"}}, {Name: "azure", Login: []string{"device"}}, {Name: "azdo", Login: []string{"pat"}}}
	if got := integrations.Catalog(); !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog=%v", got)
	}
}
