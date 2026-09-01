package prompt

import (
	"path/filepath"
	"strings"
	"testing"

	"desk/internal/plugin"
)

func TestCatalogLoadsStableSnapshot(t *testing.T) {
	dir := filepath.Join("..", "..", "prompts")
	first, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash() == "" || first.Hash() != second.Hash() {
		t.Fatalf("unstable hash %q %q", first.Hash(), second.Hash())
	}
	for _, phase := range []string{"plan", "act", "review"} {
		system := first.System(phase)
		if !strings.Contains(system, "Desk") || !strings.Contains(system, phase) {
			t.Fatalf("phase %s system=%q", phase, system)
		}
	}
}

func TestCatalogOverridesDescriptionOnly(t *testing.T) {
	snapshot, err := Load(filepath.Join("..", "..", "prompts"))
	if err != nil {
		t.Fatal(err)
	}
	parameters := []byte(`{"type":"object"}`)
	tools := snapshot.ApplyTools([]plugin.Tool{{
		Name:        "fs.read",
		Description: "old",
		Risk:        "read",
		Parameters:  parameters,
	}})
	if len(tools) != 1 || tools[0].Description == "old" {
		t.Fatalf("description not overridden: %+v", tools)
	}
	if string(tools[0].Parameters) != string(parameters) || tools[0].Risk != "read" {
		t.Fatalf("tool contract changed: %+v", tools[0])
	}
}
