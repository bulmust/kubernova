package client

import (
	"math"
	"strings"
	"testing"
	"time"

	"kubernova/internal/config"
	"kubernova/internal/model"
)

func testPromConfig() config.Prometheus {
	return config.Default().Prometheus
}

func testResources() config.Resources {
	return config.Default().Resources
}

func testPromContainers() []model.Container {
	return []model.Container{
		{Namespace: "default", Pod: "api-1", Name: "api", Owners: "Deployment", OwnerName: "api"},
		{Namespace: "default", Pod: "api-2", Name: "api", Owners: "Deployment", OwnerName: "api"},
		{Namespace: "default", Pod: "api-1", Name: "POD"},
		{Namespace: "", Pod: "ignored", Name: "api"},
	}
}

func TestBuildQueryPlanGatingAndReuse(t *testing.T) {
	prom := testPromConfig()
	prom.LabelFilters = map[string]string{"cluster": "prod"}
	resources := testResources()
	plan := BuildQueryPlan(prom, resources, testPromContainers())
	if plan.CPU == nil || plan.Memory == nil || plan.OOM == nil {
		t.Fatalf("plan groups = %#v, want CPU, memory, and OOM", plan)
	}
	if !strings.Contains(plan.CPU.Query, `namespace=~"default"`) || !strings.Contains(plan.CPU.Query, `container!="pause"`) {
		t.Errorf("CPU query selector = %s", plan.CPU.Query)
	}
	if strings.Count(plan.CPU.Query, "quantile_over_time") != 1 {
		t.Errorf("equal request/limit percentile should use one quantile: %s", plan.CPU.Query)
	}
	if plan.CPU.Range != 24*time.Hour || plan.CPU.Step <= 0 {
		t.Errorf("CPU range/step = %v/%v", plan.CPU.Range, plan.CPU.Step)
	}
	if !strings.Contains(plan.OOM.Query, `reason="OOMKilled"`) || !strings.Contains(plan.OOM.Query, "max_over_time") {
		t.Errorf("OOM query = %s", plan.OOM.Query)
	}
	if strings.Contains(plan.OOM.Query, "count_over_time") || strings.Contains(plan.OOM.Query, "image!=") {
		t.Errorf("OOM query uses an invalid count/image matcher: %s", plan.OOM.Query)
	}
	if !strings.Contains(plan.OOM.Query, `namespace=~"default"`) || !strings.Contains(plan.OOM.Query, `container!="pause"`) {
		t.Errorf("OOM query selector = %s", plan.OOM.Query)
	}
	if strings.Contains(plan.OOM.Query, `cluster=`) || !strings.Contains(plan.CPU.Query, `cluster=`) {
		t.Errorf("OOM query must not inherit cAdvisor label filters; CPU must keep them")
	}

	resources.CPU.Percentiles = config.ResourcePercentiles{Request: 90, Limit: 99}
	plan = BuildQueryPlan(prom, resources, testPromContainers())
	if strings.Count(plan.CPU.Query, "quantile_over_time") != 2 ||
		!strings.Contains(plan.CPU.Query, `"quantile", "request"`) ||
		!strings.Contains(plan.CPU.Query, `"quantile", "limit"`) {
		t.Errorf("different percentiles should be labelled in one query: %s", plan.CPU.Query)
	}

	resources.CPU.Enabled = false
	resources.Memory.OOMKilledCheck = false
	resources.Memory.Percentiles = config.ResourcePercentiles{}
	plan = BuildQueryPlan(prom, resources, testPromContainers())
	if plan.CPU != nil || plan.Memory != nil || plan.OOM != nil {
		t.Errorf("disabled groups produced plan %#v", plan)
	}

	prom.Enabled = false
	plan = BuildQueryPlan(prom, testResources(), testPromContainers())
	if plan.CPU != nil || plan.Memory != nil || plan.OOM != nil {
		t.Errorf("disabled prometheus produced plan %#v", plan)
	}
}

func TestBuildQueryPlanAppliesLabelFiltersAndExplicitStep(t *testing.T) {
	prom := testPromConfig()
	prom.LabelFilters = map[string]string{"cluster": "prod"}
	resources := testResources()
	resources.CPU.QueryStep = "1m"
	plan := BuildQueryPlan(prom, resources, testPromContainers())
	if !strings.Contains(plan.CPU.Query, `cluster="prod"`) {
		t.Errorf("label filter missing from %s", plan.CPU.Query)
	}
	if plan.CPU.Step != time.Minute || !strings.Contains(plan.CPU.Query, ":1m") {
		t.Errorf("explicit step missing: %v %s", plan.CPU.Step, plan.CPU.Query)
	}
	if plan := BuildQueryPlan(prom, resources, nil); plan.CPU != nil || plan.Memory != nil || plan.OOM != nil {
		t.Errorf("empty Kubernetes data should produce no queries: %#v", plan)
	}
}

func TestPrometheusNamespaceBatches(t *testing.T) {
	namespaces := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}
	batches := PrometheusNamespaceBatches(namespaces, PrometheusNamespaceBatchSize)
	if len(batches) != 3 {
		t.Fatalf("batch count = %d, want 3", len(batches))
	}
	want := [][]string{
		{"a", "b", "c", "d", "e"},
		{"f", "g", "h", "i", "j"},
		{"k"},
	}
	for index := range want {
		if got := strings.Join(batches[index], ","); got != strings.Join(want[index], ",") {
			t.Errorf("batch %d = %v, want %v", index, batches[index], want[index])
		}
	}
	if got := PrometheusNamespaceBatches(nil, PrometheusNamespaceBatchSize); got != nil {
		t.Errorf("empty namespace batches = %v, want nil", got)
	}
}

func TestBuildQueryPlanForNamespacesOnlySelectsBatch(t *testing.T) {
	plan := BuildQueryPlanForNamespaces(testPromConfig(), testResources(), []string{"team-a", "team-b"})
	if plan.CPU == nil {
		t.Fatal("batch plan has no CPU query")
	}
	if !strings.Contains(plan.CPU.Query, `namespace=~"team-a|team-b"`) {
		t.Errorf("batch selector = %s", plan.CPU.Query)
	}
	if strings.Contains(plan.CPU.Query, "default") {
		t.Errorf("batch selector leaked namespaces from other batches: %s", plan.CPU.Query)
	}
}

func TestSuggestionMinimumClampsGeneratedValues(t *testing.T) {
	containers := []model.Container{
		{Namespace: "default", Pod: "api-1", Name: "api", Owners: "Deployment", OwnerName: "api", CPUReq: "5m", CPULim: "5m", MemReq: "1Mi", MemLim: "1Mi"},
	}
	resources := testResources()
	resources.CPU.Percentiles = config.ResourcePercentiles{Request: 90, Limit: 90}
	resources.CPU.SuggestionBufferPercentage = config.Percentages{}
	resources.CPU.SuggestionMinimum = config.ResourceMinimum{Request: "10m", Limit: "20m"}
	resources.Memory.Percentiles = config.ResourcePercentiles{Request: 90, Limit: 90}
	resources.Memory.SuggestionBufferPercentage = config.Percentages{}
	resources.Memory.SuggestionMinimum = config.ResourceMinimum{Request: "64Mi", Limit: "128Mi"}

	plan := BuildQueryPlan(testPromConfig(), resources, containers)
	results := QueryResults{
		CPU: PrometheusResult{Result: []PrometheusSample{
			{Metric: map[string]string{"namespace": "default", "pod": "api-1", "container": "api"}, Value: 0.001},
		}},
		Memory: PrometheusResult{Result: []PrometheusSample{
			{Metric: map[string]string{"namespace": "default", "pod": "api-1", "container": "api"}, Value: 1024 * 1024},
		}},
	}
	updated := ApplyPrometheusResults(containers, plan, results, time.Unix(100, 0))
	if updated[0].Prometheus == nil {
		t.Fatal("container was not updated")
	}
	data := updated[0].Prometheus
	checks := []struct {
		name string
		got  *float64
		want float64
	}{
		{"cpu request", data.CPURequestSuggestion, 0.01},
		{"cpu limit", data.CPULimitSuggestion, 0.02},
		{"memory request", data.MemoryRequestSuggestion, 64 * 1024 * 1024},
		{"memory limit", data.MemoryLimitSuggestion, 128 * 1024 * 1024},
	}
	for _, check := range checks {
		if check.got == nil || math.Abs(*check.got-check.want) > 1e-6 {
			t.Errorf("%s suggestion = %v, want %v", check.name, check.got, check.want)
		}
	}
}

func TestOOMKilledMemoryLimitMultiplier(t *testing.T) {
	containers := []model.Container{
		{Namespace: "default", Pod: "api-1", Name: "api", Owners: "Deployment", OwnerName: "api", MemLim: "100Mi", OOM: "yes"},
		{Namespace: "default", Pod: "worker-1", Name: "worker", Owners: "Deployment", OwnerName: "worker", MemLim: "100Mi", OOM: "no"},
		{Namespace: "default", Pod: "oomsrv-1", Name: "oomsrv", Owners: "Deployment", OwnerName: "oomsrv", MemLim: "80Mi"},
		{Namespace: "default", Pod: "high-1", Name: "high", Owners: "Deployment", OwnerName: "high", MemLim: "10Mi", OOM: "yes"},
	}
	resources := testResources()
	resources.Memory.Percentiles = config.ResourcePercentiles{Request: 0, Limit: 99}
	resources.Memory.SuggestionBufferPercentage = config.Percentages{}
	resources.Memory.SuggestionMinimum = config.ResourceMinimum{}
	resources.Memory.OOMKilledLimitMultiplier = 1.5
	plan := BuildQueryPlan(testPromConfig(), resources, containers)

	limit := 50 * 1024 * 1024
	results := QueryResults{
		Memory: PrometheusResult{Result: []PrometheusSample{
			{Metric: map[string]string{"namespace": "default", "pod": "api-1", "container": "api"}, Value: float64(limit)},
			{Metric: map[string]string{"namespace": "default", "pod": "worker-1", "container": "worker"}, Value: float64(limit)},
			{Metric: map[string]string{"namespace": "default", "pod": "oomsrv-1", "container": "oomsrv"}, Value: float64(limit)},
			{Metric: map[string]string{"namespace": "default", "pod": "high-1", "container": "high"}, Value: float64(100 * 1024 * 1024)},
		}},
		OOM: PrometheusResult{Result: []PrometheusSample{
			{Metric: map[string]string{"namespace": "default", "pod": "oomsrv-1", "container": "oomsrv"}, Value: 1},
		}},
	}
	updated := ApplyPrometheusResults(containers, plan, results, time.Unix(100, 0))
	want := []float64{
		150 * 1024 * 1024, // Kubernetes OOM status raises the current 100Mi limit by 1.5.
		50 * 1024 * 1024,  // Not OOMKilled: percentile suggestion unchanged.
		120 * 1024 * 1024, // Prometheus OOM observation raises the current 80Mi limit by 1.5.
		100 * 1024 * 1024, // A higher percentile suggestion wins over current limit * 1.5.
	}
	for index, expected := range want {
		got := updated[index].Prometheus.MemoryLimitSuggestion
		if got == nil || math.Abs(*got-expected) > 1e-6 {
			t.Errorf("container %d memory limit suggestion = %v, want %v", index, got, expected)
		}
	}
}

func TestApplyPrometheusResults(t *testing.T) {
	containers := testPromContainers()
	resources := testResources()
	resources.CPU.Percentiles = config.ResourcePercentiles{Request: 90, Limit: 90}
	resources.Memory.Percentiles = config.ResourcePercentiles{Request: 90, Limit: 99}
	plan := BuildQueryPlan(testPromConfig(), resources, containers)
	now := time.Unix(100, 0)
	results := QueryResults{
		CPU: PrometheusResult{Result: []PrometheusSample{
			{Metric: map[string]string{"namespace": "default", "pod": "api-1", "container": "api"}, Value: 1},
			{Metric: map[string]string{"namespace": "default", "pod": "api-2", "container": "api"}, Value: 2},
			{Metric: map[string]string{"namespace": "other", "pod": "absent", "container": "api"}, Value: 9},
		}},
		Memory: PrometheusResult{Result: []PrometheusSample{
			{Metric: map[string]string{"namespace": "default", "pod": "api-1", "container": "api", "quantile": "request"}, Value: 64 * 1024 * 1024},
			{Metric: map[string]string{"namespace": "default", "pod": "api-2", "container": "api", "quantile": "request"}, Value: 128 * 1024 * 1024},
			{Metric: map[string]string{"namespace": "default", "pod": "api-1", "container": "api", "quantile": "limit"}, Value: 256 * 1024 * 1024},
		}},
		OOM: PrometheusResult{Result: []PrometheusSample{
			{Metric: map[string]string{"namespace": "default", "pod": "api-1", "container": "api"}, Value: 2},
			{Metric: map[string]string{"namespace": "default", "pod": "api-2", "container": "api"}, Value: 3},
		}},
	}
	updated := ApplyPrometheusResults(containers, plan, results, now)
	if updated[0].Prometheus == nil || updated[1].Prometheus == nil {
		t.Fatal("matching Kubernetes rows were not updated")
	}
	if got := *updated[0].Prometheus.CPURequestPercentile; got != 2 {
		t.Errorf("CPU request max = %v, want 2", got)
	}
	if got := *updated[0].Prometheus.CPURequestSuggestion; got != 2.2 {
		t.Errorf("CPU request suggestion = %v, want 2.2", got)
	}
	if got := *updated[0].Prometheus.CPULimitPercentile; got != 2 {
		t.Errorf("equal CPU limit percentile = %v, want 2", got)
	}
	if got := *updated[0].Prometheus.MemoryRequestPercentile; got != 128*1024*1024 {
		t.Errorf("memory request max = %v", got)
	}
	if got := *updated[0].Prometheus.MemoryLimitPercentile; got != 256*1024*1024 {
		t.Errorf("memory limit max = %v", got)
	}
	if got := *updated[0].Prometheus.MemoryLimitSuggestion; got != 256*1024*1024*1.1 {
		t.Errorf("memory limit suggestion = %v", got)
	}
	if got := *updated[0].Prometheus.OOMKills; got != 1 {
		t.Errorf("OOM observation = %v, want 1 (affected, not an event count)", got)
	}
	if updated[0].Prometheus.OOMAvailable == nil || !*updated[0].Prometheus.OOMAvailable {
		t.Error("OOM availability was not marked")
	}
	if updated[2].Prometheus != nil {
		t.Errorf("pause container should be ignored: %#v", updated[2].Prometheus)
	}
	if updated[3].Prometheus != nil {
		t.Errorf("empty namespace container should be ignored")
	}
}
