package toolchains_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

func TestSelectionExposesCanonicalNamesWithoutChangingTheSet(t *testing.T) {
	set, err := toolchains.Parse("native,azure,azure")
	if err != nil {
		t.Fatal(err)
	}
	names := set.Names()
	if !slices.Equal(names, []string{"azure", "native"}) {
		t.Fatalf("names = %v", names)
	}
	names[0] = "changed"
	if set.String() != "azure,native" {
		t.Fatalf("caller changed selection: %q", set.String())
	}
	if len((toolchains.Set{}).Names()) != 0 {
		t.Fatal("base image selection has toolchain names")
	}
}

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
	for _, value := range []string{"", "native,", "none,native", "none,none", "dotnet", "playwright", "nosuch"} {
		_, err := toolchains.Parse(value)
		if err == nil {
			t.Errorf("accepted %q", value)
			continue
		}
		if !strings.Contains(err.Error(), "valid values: azure, native, none") {
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
			return
		}
	}
	t.Fatal("native toolchain missing from catalog")
}

func TestAzureCarriesItsAgentSmokeCheck(t *testing.T) {
	for _, definition := range toolchains.Catalog() {
		if definition.Name == "azure" {
			if !definition.Delivered || definition.SmokeCheck != "az version" || definition.SmokeUser != "1000:1000" {
				t.Fatalf("Azure smoke check = %+v", definition)
			}
			return
		}
	}
	t.Fatal("Azure toolchain missing from catalog")
}
