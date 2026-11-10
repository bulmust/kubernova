package state

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSaveAndLoadFullViewState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	collapsed := true
	want := State{
		Namespaces:               []string{"argocd", "default"},
		ActiveNamespace:          "argocd",
		Columns:                  []string{"K01", "K02"},
		ReplicaCollapsed:         &collapsed,
		HiddenRows:               []string{"row-1"},
		Fullscreen:               true,
		SortColumn:               "K03",
		SortDescending:           true,
		SchemaVersion:            StateSchemaVersion,
		PrometheusURL:            "https://prom.example",
		PrometheusSSLVerify:      func() *bool { value := true; return &value }(),
		PrometheusTenantHeader:   "tenant-a",
		PrometheusBearerTokenEnv: "PROM_TOKEN",
		PrometheusLabelFilters:   map[string]string{"cluster": "prod"},
		QColumnsCustomized:       true,
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
