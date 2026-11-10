package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	collapsed := true
	want := State{
		SchemaVersion:    StateSchemaVersion,
		Namespaces:       []string{"argocd", "default"},
		ActiveNamespace:  "argocd",
		Columns:          []string{"CLUSTER", "NAMESPACE"},
		ReplicaCollapsed: &collapsed,
		HiddenRows:       []string{"row-1"},
	}
	if err := Save("prod/cluster", want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	path, err := Path("prod/cluster")
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if wantPath := filepath.Join(dir, "kubernova", "clusters", "prod_cluster", "state.yaml"); path != wantPath {
		t.Errorf("Path() = %q, want %q", path, wantPath)
	}

	got, err := Load("prod/cluster")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %#v, want %#v", got, want)
	}
}

func TestSavePreservesEmptyColumnList(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := Save("cluster", State{Columns: []string{}}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load("cluster")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Columns == nil {
		t.Fatal("Load() returned a nil column list, want an empty non-nil list")
	}
	if len(got.Columns) != 0 {
		t.Errorf("columns = %v, want empty", got.Columns)
	}
}

func TestLoadMissingReturnsEmptyState(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	got, err := Load("missing")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got.Namespaces) != 0 || got.ActiveNamespace != "" {
		t.Errorf("Load() = %#v, want empty state", got)
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := Save("cluster", State{ActiveNamespace: "all"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	path, err := Path("cluster")
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("state file is missing: %v", err)
	}
}

func TestLegacyEnabledIsIgnoredAndSaveUsesTwoSpaces(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := Path("cluster")
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := "enabled: false\nnamespaces:\n- argocd\nactive_namespace: argocd\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	loaded, err := Load("cluster")
	if err != nil {
		t.Fatalf("Load() error = %v, want legacy enabled ignored", err)
	}
	if loaded.ActiveNamespace != "argocd" {
		t.Errorf("active namespace = %q, want argocd", loaded.ActiveNamespace)
	}
	if err := Save("cluster", loaded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(saved), "\n    ") {
		t.Errorf("state YAML was not written with two-space indentation:\n%s", saved)
	}
}
