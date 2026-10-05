package toolchains

import (
	"fmt"
	"slices"
	"strings"
)

type Set struct{ names string }

func (set Set) String() string { return set.names }

func Display(names string) string {
	if names == "" {
		return "none"
	}
	return names
}

func (set Set) Names() []string {
	if set.names == "" {
		return nil
	}
	return strings.Split(set.names, ",")
}

func Parse(value string) (Set, error) {
	names := strings.Split(value, ",")
	if len(names) == 1 && names[0] == "none" {
		return Set{}, nil
	}
	if slices.Contains(names, "none") {
		return Set{}, fmt.Errorf("none must stand alone; valid values: %s", strings.Join(ValidValues(), ", "))
	}
	for _, name := range names {
		if !delivered(name) {
			return Set{}, fmt.Errorf("invalid toolchain %q; valid values: %s", name, strings.Join(ValidValues(), ", "))
		}
	}
	slices.Sort(names)
	return Set{names: strings.Join(slices.Compact(names), ",")}, nil
}

type Definition struct {
	Name       string
	Delivered  bool
	SmokeCheck string
	SmokeUser  string
}

func Catalog() []Definition {
	return []Definition{
		{Name: "dotnet", Delivered: true, SmokeCheck: "dotnet --list-sdks", SmokeUser: "1000:1000"},
		{Name: "playwright"},
		{Name: "azure", Delivered: true, SmokeCheck: "az version", SmokeUser: "1000:1000"},
		{Name: "native", Delivered: true, SmokeCheck: "sh /usr/local/share/sandboxed-agents/smoke/native.sh", SmokeUser: "1000:1000"},
	}
}

func ValidValues() []string {
	values := []string{"none"}
	for _, definition := range Catalog() {
		if definition.Delivered {
			values = append(values, definition.Name)
		}
	}
	slices.Sort(values)
	return values
}

func delivered(name string) bool {
	for _, definition := range Catalog() {
		if definition.Name == name {
			return definition.Delivered
		}
	}
	return false
}
