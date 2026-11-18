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
		"Containers severity HIGH",
		"Containers severity MEDIUM",
		"Containers severity OK",
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

func TestRenderMarkdownPadded(t *testing.T) {
	cache, value, at := fixture()
	raw := RenderMarkdownWithOptions(value, cache.Containers(), false, at, MarkdownOptions{Padded: true})
	text := string(raw)
	lines := strings.Split(text, "\n")
	var summary, rule, firstRow string
	for index, line := range lines {
		if line == "| Field" || strings.HasPrefix(line, "| Field ") {
			summary = line
			rule = lines[index+1]
			firstRow = lines[index+2]
			break
		}
	}
	if summary == "" {
		t.Fatalf("padded Markdown has no summary table:\n%s", text)
	}
	if len(rule) != len(summary) || len(firstRow) != len(summary) {
		t.Errorf("padded rows have different widths:\n%s\n%s\n%s", summary, rule, firstRow)
	}
	if !strings.Contains(firstRow, "| Cluster ") {
		t.Errorf("padded first summary row = %q", firstRow)
	}
	if strings.HasPrefix(rule, "| --- |") {
		t.Errorf("padded separator was not widened: %q", rule)
	}
}

func TestDirForResolvesDefaultTemplate(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	dir, err := DirFor("clusters/<cluster>/<context>/exports/", "prod", "ctx/one")
	if err != nil {
		t.Fatalf("DirFor() error = %v", err)
	}
	want := filepath.Join(base, "kubernova", "clusters", "prod", "ctx_one", "exports")
	if dir != want {
		t.Errorf("DirFor() = %q, want %q", dir, want)
	}
}

func TestFileNameFromTemplate(t *testing.T) {
	at := time.Date(2026, 11, 10, 12, 30, 0, 0, time.UTC)
	got := FileNameFromTemplate("report-<cluster>-<context>-<timestamp>", "prod", "ctx", at, "md")
	if got != "report-prod-ctx-261110-123000.md" {
		t.Errorf("FileNameFromTemplate() = %q", got)
	}
	if got := FileNameFromTemplate("report.json", "prod", "ctx", at, "md"); got != "report.md" {
		t.Errorf("FileNameFromTemplate() replaced extension = %q, want report.md", got)
	}
}

func TestExportSelectedColumns(t *testing.T) {
	cache, value, at := fixture()
	columns := []int{columnIndexOf("K02"), columnIndexOf("K08"), columnIndexOf("P10")}
	markdown := string(RenderMarkdownWithOptions(value, cache.Containers(), false, at, MarkdownOptions{Columns: columns}))
	if !strings.Contains(markdown, "NAMESPACE (K02)") || !strings.Contains(markdown, "CONTAINER (K08)") ||
		!strings.Contains(markdown, "SEVERITY (P10)") {
		t.Errorf("selected columns are missing:\n%s", markdown)
	}
	if strings.Contains(markdown, "CPU REQ (K09)") || strings.Contains(markdown, "POD (K05)") {
		t.Errorf("unselected columns were exported:\n%s", markdown)
	}
	raw, err := RenderJSONWithOptions(value, cache, false, at, JSONOptions{Columns: columns})
	if err != nil {
		t.Fatalf("RenderJSONWithOptions() error = %v", err)
	}
	text := string(raw)
	for _, want := range []string{`"namespace":`, `"container":`, `"severity": "OK"`} {
		if !strings.Contains(text, want) {
			t.Errorf("JSON is missing selected column %s", want)
		}
	}
	for _, unwanted := range []string{`"cpu_req":`, `"mem_lim":`} {
		if strings.Contains(text, unwanted) {
			t.Errorf("JSON contains unselected column %s", unwanted)
		}
	}
}

func columnIndexOf(id string) int {
	for index, column := range model.Columns {
		if column.ID == id {
			return index
		}
	}
	return -1
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
	if got := FileName("prod", "ctx/one", at, "json"); got != "kubernova-prod-ctx_one-261110-123000.json" {
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
