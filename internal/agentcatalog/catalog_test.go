package agentcatalog_test

import (
	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"reflect"
	"testing"
)

func TestEmbeddedCatalogDeliversFourAgents(t *testing.T) {
	catalog := agentcatalog.Embedded()
	if got := catalog.Names(); !reflect.DeepEqual(got, []string{"claude", "codex", "copilot", "opencode"}) {
		t.Fatalf("names=%v", got)
	}
	packages := map[string]string{"claude": "@anthropic-ai/claude-code", "codex": "@openai/codex", "copilot": "@github/copilot", "opencode": "opencode-ai"}
	for name, pkg := range packages {
		entry, ok := catalog.Find(name)
		if !ok || entry.Command != name || entry.Install.Kind != "npm" || entry.Install.Package != pkg || len(entry.LoginWorkflows) == 0 || entry.LoginMessage == "" || entry.Documentation == "" {
			t.Fatalf("entry=%+v", entry)
		}
	}
}

func TestCatalogRejectsUnsafeOrAmbiguousInstallationData(t *testing.T) {
	for _, document := range []string{
		`{"schema_version":2,"entries":[]}`,
		`{"schema_version":1,"entries":[{"name":"bad","command":"bad","install":{"kind":"shell","package":"bad"}}]}`,
		`{"schema_version":1,"entries":[{"name":"bad","command":"bad","install":{"kind":"npm","package":"--config=evil"}}]}`,
		`{"schema_version":1,"entries":[{"name":"bad","command":"/home/agent/evil","install":{"kind":"npm","package":"bad"}}]}`,
		`{"schema_version":1,"entries":[{"name":"bad","command":"bad","install":{"kind":"npm","package":"../bad"}}]}`,
		`{"schema_version":1,"entries":[{"name":"bad","command":"bad","install":{"kind":"npm","package":"bad"}},{"name":"bad","command":"bad","install":{"kind":"npm","package":"other"}}]}`,
		`{"schema_version":1,"entries":[]} {}`,
		`{"schema_version":1,"entries":[],"unknown":true}`,
	} {
		if _, err := agentcatalog.Load([]byte(document)); err == nil {
			t.Fatalf("accepted invalid catalog %s", document)
		}
	}
}
