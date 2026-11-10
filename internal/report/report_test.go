package report

import (
	"strings"
	"testing"
	"time"

	"kubernova/internal/client"
	"kubernova/internal/config"
	"kubernova/internal/model"
)

func floatPointer(value float64) *float64 { return &value }

func prometheusData() *model.PrometheusData {
	oom := int64(2)
	return &model.PrometheusData{
		Source:                  "prometheus",
		CPURequestSuggestion:    floatPointer(0.2),
		CPULimitSuggestion:      floatPointer(0.5),
		MemoryRequestSuggestion: floatPointer(64 * 1024 * 1024),
		MemoryLimitSuggestion:   floatPointer(256 * 1024 * 1024),
		OOMKills:                &oom,
	}
}

func reportCache() client.Cache {
	containers := make([]model.Container, 0, 3)
	for index := 0; index < 3; index++ {
		containers = append(containers, model.Container{
			Cluster: "prod", Namespace: "default", Owners: "Deployment", OwnerName: "api",
			Pod: "api-" + string(rune('0'+index)), Name: "api", Image: "api:v1",
			CPUReq: "100m", CPULim: "500m", MemReq: "64Mi", MemLim: "128Mi",
			OOM: "yes", Replicas: "3", Prometheus: prometheusData(),
			Status: &model.ContainerStatus{State: "Running", Running: true},
		})
	}
	cache := client.BuildCache("prod", "ctx", containers, time.Unix(100, 0))
	cache.Nodes = &client.NodeInventory{
		Available: true, TotalNodes: 2, TotalCPUCapacity: "6", TotalAllocatableCPU: "5800m",
		TotalMemoryCapacity: "12Gi", TotalAllocatableMemory: "10Gi",
		Items: []client.CachedNode{
			{Name: "worker-1", CapacityCPU: "4", CapacityMemory: "8Gi", AllocatableCPU: "3900m", AllocatableMemory: "7Gi"},
			{Name: "worker-2", CapacityCPU: "2", CapacityMemory: "4Gi", AllocatableCPU: "1900m", AllocatableMemory: "3Gi"},
		},
	}
	cache.Prometheus = &client.CachePrometheus{Host: "prom.example", FetchedAt: time.Unix(100, 0)}
	return cache
}

func TestBuildSummaryTotals(t *testing.T) {
	cfg := config.Default()
	report := Build(reportCache(), cfg, Metadata{PrometheusURL: "https://user:pass@prom.example/path", ExportDate: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)})
	summary := report.Summary
	if summary.Cluster != "prod" || summary.Context != "ctx" {
		t.Errorf("identity = %s/%s", summary.Cluster, summary.Context)
	}
	if summary.TotalNodes != "2" || summary.TotalCPU != "6" || summary.TotalAllocatableCPU != "5800m" ||
		summary.TotalMemory != "12Gi" || summary.TotalAllocatableMemory != "10Gi" {
		t.Errorf("node totals = %+v", summary)
	}
	if summary.TotalPods != 3 || summary.TotalContainers != 3 || summary.TotalWorkloads != 1 {
		t.Errorf("counts = pods %d containers %d workloads %d", summary.TotalPods, summary.TotalContainers, summary.TotalWorkloads)
	}
	if summary.WorkloadsNeedingAdjustment != 1 || summary.OOMAffectedWorkloadsKube != 1 || summary.OOMAffectedContainersProm == nil || *summary.OOMAffectedContainersProm != 3 {
		t.Errorf("workload counts = %+v", summary)
	}
	if summary.CPURequest.Current != "300m" || summary.CPURequest.Suggested != "600m" ||
		summary.CPURequest.Change != "+300m" || summary.CPURequest.ChangePercent != "+100.0%" ||
		summary.CPURequest.ChangesNeeded != 1 {
		t.Errorf("CPU request total = %+v", summary.CPURequest)
	}
	if summary.CPULimit.Current != "1500m" || summary.CPULimit.Suggested != "1500m" || summary.CPULimit.ChangesNeeded != 0 {
		t.Errorf("CPU limit total = %+v", summary.CPULimit)
	}
	if summary.MemoryRequest.Current != "192Mi" || summary.MemoryRequest.Suggested != "192Mi" {
		t.Errorf("memory request total = %+v", summary.MemoryRequest)
	}
	if summary.MemoryLimit.Current != "384Mi" || summary.MemoryLimit.Suggested != "768Mi" || summary.MemoryLimit.ChangesNeeded != 1 {
		t.Errorf("memory limit total = %+v", summary.MemoryLimit)
	}
	if summary.PrometheusURL != "https://prom.example/path" {
		t.Errorf("sanitized URL = %q", summary.PrometheusURL)
	}
	if summary.PrometheusMode != "cached stale" {
		t.Errorf("prometheus mode = %q, want cached stale", summary.PrometheusMode)
	}
	if summary.LimitRangeContainers != 0 {
		t.Errorf("limit range containers = %d, want 0", summary.LimitRangeContainers)
	}
	if summary.CPUAllocatableComparison != "600m / 5800m (10.3% of allocatable)" {
		t.Errorf("CPU allocatable comparison = %q", summary.CPUAllocatableComparison)
	}
	if summary.MemoryAllocatableComparison != "192Mi / 10Gi (1.9% of allocatable)" {
		t.Errorf("memory allocatable comparison = %q", summary.MemoryAllocatableComparison)
	}
	if summary.ExportDate != "2026-11-10T12:00:00Z" {
		t.Errorf("export date = %q", summary.ExportDate)
	}
	if !summary.CPURequest.Known {
		t.Error("CPU request total marked unknown with Prometheus data")
	}
	if len(report.Nodes) != 2 || report.Nodes[0].AllocatableMemory != "7Gi" {
		t.Errorf("nodes = %#v", report.Nodes)
	}
}

func TestBuildWithoutPrometheusUsesUnknownValues(t *testing.T) {
	containers := []model.Container{{
		Cluster: "prod", Namespace: "default", Owners: "Deployment", OwnerName: "api",
		Pod: "api-0", Name: "api", CPUReq: "100m", CPULim: "200m", MemReq: "64Mi", MemLim: "128Mi",
	}}
	cache := client.BuildCache("prod", "ctx", containers, time.Unix(100, 0))
	report := Build(cache, config.Default(), Metadata{})
	summary := report.Summary
	if summary.CPURequest.Known || summary.CPURequest.Current != "-" || summary.CPURequest.ChangesNeeded != 0 {
		t.Errorf("CPU request without Prometheus = %+v", summary.CPURequest)
	}
	if summary.PrometheusMode != "-" || summary.PrometheusURL != "-" {
		t.Errorf("prometheus fields = %q/%q", summary.PrometheusMode, summary.PrometheusURL)
	}
	if summary.OOMAffectedContainersProm != nil {
		t.Errorf("Prometheus OOM container count = %v, want null", *summary.OOMAffectedContainersProm)
	}
	if summary.TotalNodes != "n/a" || summary.TotalCPU != "n/a" || summary.TotalMemory != "n/a" {
		t.Errorf("unknown node fields = %+v", summary)
	}
	if summary.TotalPods != 1 || summary.TotalContainers != 1 || summary.TotalWorkloads != 1 {
		t.Errorf("counts = %+v", summary)
	}
}

func TestNeedsChangeThresholdAndMissingCurrent(t *testing.T) {
	tests := []struct {
		name      string
		current   float64
		currentOK bool
		suggested float64
		want      bool
	}{
		{"below threshold", 100, true, 105, false},
		{"above threshold", 100, true, 111, true},
		{"at threshold", 100, true, 110, false},
		{"negative direction", 100, true, 85, true},
		{"missing current positive suggestion", 0, false, 10, true},
		{"missing current zero suggestion", 0, false, 0, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := needsChange(test.current, test.currentOK, test.suggested); got != test.want {
				t.Errorf("needsChange() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBuildCountsMissingCurrentValues(t *testing.T) {
	containers := []model.Container{{
		Cluster: "prod", Namespace: "default", Owners: "Deployment", OwnerName: "api",
		Pod: "api-0", Name: "api", CPUReq: "-", CPULim: "500m", MemReq: "64Mi", MemLim: "128Mi",
		Prometheus: &model.PrometheusData{CPURequestSuggestion: floatPointer(0.2)},
	}}
	cache := client.BuildCache("prod", "ctx", containers, time.Unix(100, 0))
	report := Build(cache, config.Default(), Metadata{})
	if report.Summary.CPURequest.MissingCurrent != 1 {
		t.Errorf("missing current = %d, want 1", report.Summary.CPURequest.MissingCurrent)
	}
	if report.Summary.CPURequest.Current != "0m" || report.Summary.CPURequest.Suggested != "200m" {
		t.Errorf("missing-current totals = %+v", report.Summary.CPURequest)
	}
	if report.Summary.CPURequest.ChangesNeeded != 1 {
		t.Errorf("missing-current changes = %d, want 1", report.Summary.CPURequest.ChangesNeeded)
	}
}

func TestBuildQueryRanges(t *testing.T) {
	cfg := config.Default()
	cfg.Resources.CPU.Range = config.Duration(time.Hour)
	cfg.Resources.Memory.Range = config.Duration(2 * time.Hour)
	cache := client.BuildCache("prod", "ctx", nil, time.Unix(100, 0))
	report := Build(cache, cfg, Metadata{})
	if report.Summary.CPUQueryRange != "1h0m0s" || report.Summary.MemoryQueryRange != "2h0m0s" {
		t.Errorf("ranges = %q/%q", report.Summary.CPUQueryRange, report.Summary.MemoryQueryRange)
	}
	var methodology strings.Builder
	for _, row := range report.Suggestion {
		methodology.WriteString(row.Resource)
		methodology.WriteString(": ")
		methodology.WriteString(row.Formula)
		methodology.WriteByte('\n')
	}
	if !strings.Contains(methodology.String(), "buffer") {
		t.Error("methodology does not describe buffers")
	}
	if report.Summary.RateWindow != "5m0s" {
		t.Errorf("rate window = %q, want 5m0s", report.Summary.RateWindow)
	}
}

func TestQueriesContainPrometheusColumns(t *testing.T) {
	report := Build(reportCache(), config.Default(), Metadata{})
	if len(report.Queries) != 1 || report.Queries[0].Name != "Prometheus" {
		t.Fatalf("query sections = %#v", report.Queries)
	}
	columns := make(map[string]ColumnQuery)
	for _, column := range report.Queries[0].Columns {
		columns[column.ColumnID] = column
	}
	for _, id := range []string{"P01", "P02", "P03", "P04", "P05", "P06", "P07", "P08", "P09"} {
		if columns[id].ColumnID == "" {
			t.Errorf("missing query for %s", id)
		}
	}
	if !strings.Contains(columns["P01"].Query, "container_cpu_usage_seconds_total") ||
		!strings.Contains(columns["P01"].Query, "rate(") {
		t.Errorf("P01 query = %q", columns["P01"].Query)
	}
	if !strings.Contains(columns["P05"].Query, "container_memory_working_set_bytes") {
		t.Errorf("P05 query = %q", columns["P05"].Query)
	}
	if !strings.Contains(columns["P09"].Query, "OOMKilled") {
		t.Errorf("P09 query = %q", columns["P09"].Query)
	}
	if columns["P02"].Note == "" || columns["P09"].Note == "" {
		t.Errorf("suggestion/OOM notes missing: %#v", columns)
	}
}

func TestPrometheusOOMAvailability(t *testing.T) {
	available := true
	unavailable := false
	cpu := 0.1
	build := func(data *model.PrometheusData) *int {
		containers := []model.Container{{
			Cluster: "prod", Namespace: "default", Owners: "Deployment", OwnerName: "api",
			Pod: "api-0", Name: "api", CPUReq: "100m", Prometheus: data,
		}}
		cache := client.BuildCache("prod", "ctx", containers, time.Unix(100, 0))
		report := Build(cache, config.Default(), Metadata{})
		return report.Summary.OOMAffectedContainersProm
	}

	if got := build(&model.PrometheusData{CPURequestSuggestion: &cpu, OOMAvailable: &unavailable}); got != nil {
		t.Errorf("unavailable OOM metric = %v, want null", *got)
	}
	if got := build(&model.PrometheusData{CPURequestSuggestion: &cpu, OOMAvailable: &available}); got == nil || *got != 0 {
		t.Errorf("available OOM metric with no affected containers = %v, want 0", got)
	}
	affected := int64(1)
	if got := build(&model.PrometheusData{CPURequestSuggestion: &cpu, OOMAvailable: &available, OOMKills: &affected}); got == nil || *got != 1 {
		t.Errorf("affected OOM containers = %v, want 1", got)
	}
}
