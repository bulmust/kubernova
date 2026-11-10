package export

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kubernova/internal/client"
	"kubernova/internal/config"
	"kubernova/internal/model"
	"kubernova/internal/report"
)

func fixture() (client.Cache, report.Report, time.Time) {
	at := time.Date(2026, 11, 10, 12, 30, 0, 0, time.UTC)
	suggestion := 0.2
	containers := []model.Container{{
		Cluster: "prod", Namespace: "default", Owners: "Deployment", OwnerName: "api",
		Pod: "api-0", Name: "api", Image: "api:v1", Registry: "docker.io", Repository: "api", Tag: "v1",
		CPUReq: "100m", CPULim: "500m", MemReq: "64Mi", MemLim: "128Mi", Replicas: "1",
		Status:     &model.ContainerStatus{State: "Running", Running: true},
		Prometheus: &model.PrometheusData{CPURequestSuggestion: &suggestion},
	}}
	cache := client.BuildCache("prod", "ctx", containers, at)
	cache.Prometheus = &client.CachePrometheus{Host: "prom.example", FetchedAt: at}
	value := report.Build(cache, config.Default(), report.Metadata{PrometheusURL: "https://prom.example", ExportDate: at})
	return cache, value, at
}

func TestRenderJSONGolden(t *testing.T) {
	cache, value, at := fixture()
	raw, err := RenderJSON(value, cache, true, at)
	if err != nil {
		t.Fatalf("RenderJSON() error = %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`"summary": {`,
		`"cluster": "prod"`,
		`"suggestion": [`,
		`"resource": "CPU request"`,
		`"formula":`,
		`"schema_version": 6`,
		`"cached": true`,
		`"sources": {`,
		`"workloads": [`,
		`"owner_kind": "Deployment"`,
		`"columns": {`,
		`"cpu_req": "100m"`,
		`"registry": "docker.io"`,
		`"status": {`,
		`"prometheus": {`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("JSON is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"prometheus_mode"`) {
		t.Errorf("JSON still contains prometheus_mode:\n%s", text)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

func TestRenderMarkdownGolden(t *testing.T) {
	cache, value, at := fixture()
	containers := cache.Containers()
	raw := RenderMarkdown(value, containers, true, at)
	text := string(raw)
	for _, want := range []string{
		"# Kubernova report: prod / ctx",
		"_Source: cached table snapshot",
		"## Summary",
		"| Query interval | 24h0m0s |",
		"| Total CPU Req->Sug | 100m -> 200m |",
		"| Total CPU Request Change Suggestion | +100m (+100.0%) |",
		"| Rate window | 5m0s |",
		"Containers using LimitRange resources",
		"OOM-affected containers (Prometheus)",
		"OOM-affected workloads (Kubernetes API)",
		"CPU request suggestion vs allocatable CPU",
		"## Suggestion",
		"| Resource | Formula |",
		"## Queries",
		"### Prometheus",
		"CPU_REQ_USE",
		"```promql",
		"## Containers",
		"| CLUSTER (K01) |",
		"| prod |",
		"| REGISTRY (K18) |",
		"| docker.io |",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Markdown is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Prometheus mode") {
		t.Errorf("Markdown still contains Prometheus mode:\n%s", text)
	}
}

func TestWriteAtomicCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "report.json")
	if err := WriteAtomic(path, []byte("data")); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(raw) != "data" {
		t.Errorf("content = %q", raw)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".export-") {
			t.Errorf("temporary export file was left behind: %s", entry.Name())
		}
	}
}

func TestDefaultNamesIncludeContextIdentity(t *testing.T) {
	at := time.Date(2026, 11, 10, 12, 30, 0, 0, time.UTC)
	if got := FileName("prod", "ctx/one", at, "json"); got != "kubernova-prod-ctx_one-20261110-123000.json" {
		t.Errorf("FileName() = %q", got)
	}
	format, err := ParsedFormat("markdown")
	if err != nil || format != FormatMarkdown {
		t.Errorf("ParsedFormat(markdown) = %q, %v", format, err)
	}
}

func TestExportIncludesEveryColumnIncludingHiddenOnes(t *testing.T) {
	cache, value, at := fixture()
	text := string(RenderMarkdown(value, cache.Containers(), false, at))
	header := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "| NAMESPACE (K02)") {
			header = line
		}
	}
	if header == "" {
		t.Fatalf("Markdown has no table header starting with the first column:\n%s", text)
	}
	last := -1
	for _, column := range model.Columns {
		cell := " " + column.Header + " (" + column.ID + ") |"
		index := strings.Index(header, cell)
		if index < 0 {
			t.Errorf("Markdown header is missing %s (hidden by default: %v)", column.ID, column.HiddenByDefault)
		}
		if index < last {
			t.Errorf("Markdown column %s is out of order", column.ID)
		}
		last = index
	}

	raw, err := RenderJSON(value, cache, false, at)
	if err != nil {
		t.Fatalf("RenderJSON() error = %v", err)
	}
	for _, column := range model.Columns {
		if want := `"` + columnKey(column) + `":`; !strings.Contains(string(raw), want) {
			t.Errorf("JSON is missing column key %s", want)
		}
	}
}
