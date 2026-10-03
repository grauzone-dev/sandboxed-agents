package toolchains_test

import (
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

func TestSelectDeliveredToolchainsAsASet(t *testing.T) {
	set, err := toolchains.Parse("native,native")
	if err != nil {
		t.Fatal(err)
	}
	if set.String() != "native" {
		t.Fatalf("selection = %q", set.String())
	}
	base, err := toolchains.Parse("none")
	if err != nil || base.String() != "" {
		t.Fatalf("base = %v, %v", base, err)
	}
}

func TestRejectUndeliveredAndInvalidSelections(t *testing.T) {
	for _, value := range []string{"", "native,", "none,native", "none,none", "dotnet", "playwright", "azure", "nosuch"} {
		_, err := toolchains.Parse(value)
		if err == nil {
			t.Errorf("accepted %q", value)
			continue
		}
		if !strings.Contains(err.Error(), "valid values: native, none") {
			t.Errorf("selection %q error = %v", value, err)
		}
	}
}

func TestCatalogCarriesDeliveredNativeBuildToolsAndSmokeCheck(t *testing.T) {
	definitions := toolchains.Catalog()
	if len(definitions) != 4 {
		t.Fatalf("catalog = %v", definitions)
	}
	for _, definition := range definitions {
		if definition.Name == "native" {
			if !definition.Delivered || definition.SmokeCheck == "" {
				t.Fatalf("native = %+v", definition)
			}
		} else if definition.Delivered {
			t.Errorf("undelivered toolchain %s is enabled", definition.Name)
		}
	}
}
