package agentcatalog

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
)

//go:embed catalog.json
var data []byte

type Install struct {
	Kind    string `json:"kind"`
	Package string `json:"package"`
}
type Probe struct {
	Args         []string `json:"args"`
	BooleanField string   `json:"boolean_field"`
}
type Entry struct {
	Name           string              `json:"name"`
	Delivered      bool                `json:"delivered"`
	Command        string              `json:"command"`
	Install        Install             `json:"install"`
	LoginWorkflows map[string][]string `json:"login_workflows"`
	LoginMessage   string              `json:"login_message"`
	StatusProbe    *Probe              `json:"status_probe,omitempty"`
	Documentation  string              `json:"documentation"`
}
type Catalog struct{ entries map[string]Entry }

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var packagePattern = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)

func Load(data []byte) (Catalog, error) {
	var document struct {
		SchemaVersion int     `json:"schema_version"`
		Entries       []Entry `json:"entries"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Catalog{}, fmt.Errorf("read agent catalog: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Catalog{}, fmt.Errorf("agent catalog must contain one JSON document")
	}
	if document.SchemaVersion != 1 {
		return Catalog{}, fmt.Errorf("unsupported agent catalog schema %d", document.SchemaVersion)
	}
	catalog := Catalog{entries: make(map[string]Entry)}
	for _, entry := range document.Entries {
		if !namePattern.MatchString(entry.Name) || !namePattern.MatchString(entry.Command) {
			return Catalog{}, fmt.Errorf("invalid agent catalog name or command %q", entry.Name)
		}
		if _, exists := catalog.entries[entry.Name]; exists {
			return Catalog{}, fmt.Errorf("duplicate agent catalog name %q", entry.Name)
		}
		if entry.Install.Kind != "npm" || !packagePattern.MatchString(entry.Install.Package) {
			return Catalog{}, fmt.Errorf("unsupported or invalid install entry for %q", entry.Name)
		}
		catalog.entries[entry.Name] = entry
	}
	return catalog, nil
}
func Embedded() Catalog {
	catalog, err := Load(data)
	if err != nil {
		panic(err)
	}
	return catalog
}
func (c Catalog) Find(name string) (Entry, bool) {
	entry, ok := c.entries[name]
	return entry, ok && entry.Delivered
}
func (c Catalog) Names() []string {
	names := []string{}
	for name, entry := range c.entries {
		if entry.Delivered {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
