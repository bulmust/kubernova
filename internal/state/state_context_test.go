package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContextStatePathAndMigration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	legacy := State{ActiveNamespace: "legacy"}
	if err := Save("prod", legacy); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := LoadFor("prod", "ctx-a")
	if err != nil {
		t.Fatalf("LoadFor() error = %v", err)
	}
	if got.ActiveNamespace != "legacy" {
		t.Errorf("migrated state namespace = %q, want legacy", got.ActiveNamespace)
	}

	contextState := State{ActiveNamespace: "context"}
	if err := SaveFor("prod", "ctx-a", contextState); err != nil {
		t.Fatalf("SaveFor() error = %v", err)
	}
	path, err := PathFor("prod", "ctx-a")
	if err != nil {
		t.Fatalf("PathFor() error = %v", err)
	}
	want := filepath.Join(dir, "kubernova", "clusters", "prod", "ctx-a", "state.yaml")
	if path != want {
		t.Errorf("PathFor() = %q, want %q", path, want)
	}
	got, err = LoadFor("prod", "ctx-a")
	if err != nil {
		t.Fatalf("LoadFor() error = %v", err)
	}
	if got.ActiveNamespace != "context" {
		t.Errorf("context state namespace = %q, want context", got.ActiveNamespace)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("context state file is missing: %v", err)
	}
}
