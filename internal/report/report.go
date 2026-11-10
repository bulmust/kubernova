// Package report builds a pure, read-only report from cached Kubernetes and
// Prometheus observations. It performs no I/O.
package report

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"kubernova/internal/client"
	"kubernova/internal/config"
	"kubernova/internal/model"
)

// DefaultChangeThreshold is the relative difference that counts as a needed
// change. It is applied to a container's current and suggested values after
// per-pod aggregation.
const DefaultChangeThreshold = 0.10

// Metadata carries state-derived values that are not part of the cache or
// global configuration.
type Metadata struct {
	PrometheusURL  string
	PrometheusLive bool
	ExportDate     time.Time
}

// SuggestionRow is one table row describing how a value is derived.
type SuggestionRow struct {
	Resource string `json:"resource"`
	Formula  string `json:"formula"`
}

// QuerySection groups the PromQL used by the Prometheus-backed columns.
type QuerySection struct {
	Name    string        `json:"name"`
	Columns []ColumnQuery `json:"columns"`
}

// ColumnQuery records the query and post-processing for one table column.
type ColumnQuery struct {
	ColumnID string `json:"column_id"`
	Header   string `json:"header"`
	Query    string `json:"query"`
	Note     string `json:"note,omitempty"`
}

// Report is the complete pure report model.
type Report struct {
	Summary    Summary         `json:"summary"`
	Suggestion []SuggestionRow `json:"suggestion"`
	Queries    []QuerySection  `json:"queries"`
	Nodes      []Node          `json:"nodes"`
}

// Node is the non-secret node inventory exported by the report.
type Node struct {
	Name              string `json:"name"`
	CapacityCPU       string `json:"capacity_cpu"`
	CapacityMemory    string `json:"capacity_memory"`
	AllocatableCPU    string `json:"allocatable_cpu"`
	AllocatableMemory string `json:"allocatable_memory"`
}

// SuggestionTotal is one CPU or memory request/limit aggregate.
type SuggestionTotal struct {
	Known          bool   `json:"known"`
	Current        string `json:"current"`
	Suggested      string `json:"suggested"`
	Change         string `json:"change"`
	ChangePercent  string `json:"change_percent"`
	MissingCurrent int    `json:"missing_current"`
	ChangesNeeded  int    `json:"changes_needed"`
}

// Summary contains the report totals in display order.
type Summary struct {
	Cluster    string `json:"cluster"`
	Context    string `json:"context"`
	ExportDate string `json:"export_date"`

	PrometheusURL    string `json:"prometheus_url"`
	PrometheusMode   string `json:"-"`
	CPUQueryRange    string `json:"cpu_query_range"`
	MemoryQueryRange string `json:"memory_query_range"`
	RateWindow       string `json:"rate_window"`

	TotalNodes             string `json:"total_nodes"`
	TotalCPU               string `json:"total_cpu"`
	TotalAllocatableCPU    string `json:"total_allocatable_cpu"`
	TotalMemory            string `json:"total_memory"`
	TotalAllocatableMemory string `json:"total_allocatable_memory"`
	TotalPods              int    `json:"total_pods"`
	TotalContainers        int    `json:"total_containers"`
	TotalWorkloads         int    `json:"total_workloads"`
	LimitRangeContainers   int    `json:"limit_range_containers"`

	WorkloadsNeedingAdjustment int  `json:"workloads_needing_adjustment"`
	OOMAffectedWorkloadsKube   int  `json:"oom_affected_workloads_kubernetes"`
	OOMAffectedContainersProm  *int `json:"oom_affected_containers_prometheus"`

	CPURequest    SuggestionTotal `json:"cpu_request"`
	CPULimit      SuggestionTotal `json:"cpu_limit"`
	MemoryRequest SuggestionTotal `json:"memory_request"`
	MemoryLimit   SuggestionTotal `json:"memory_limit"`

	CPUAllocatableComparison    string `json:"cpu_suggested_vs_allocatable"`
	MemoryAllocatableComparison string `json:"memory_suggested_vs_allocatable"`
}

type metric int

const (
	metricCPURequest metric = iota
	metricCPULimit
	metricMemoryRequest
	metricMemoryLimit
)

// Build derives a Report from cached data, global configuration, and the
// state-derived metadata. Definitions:
//   - Req->Sug sums current values and suggested values separately.
//   - Change suggestions are signed (suggested-current) with a percentage of
//     current; a missing current value is treated as zero and counted.
//   - A change is needed when abs(suggested-current)/current exceeds
//     DefaultChangeThreshold. A missing current value with a positive
//     suggestion also needs a change.
//   - Values are aggregated by workload owner and container name: the maximum
//     pod sample is treated as one container and multiplied by the workload's
//     cached pod count.
//   - CPU totals round to millicores and memory totals round to MiB.
func Build(cache client.Cache, cfg config.Config, metadata Metadata) Report {
	exportAt := metadata.ExportDate
	if exportAt.IsZero() {
		exportAt = time.Now()
	}
	report := Report{
		Summary: Summary{
			Cluster:                     cache.Cluster.Cluster,
			Context:                     cache.Cluster.Context,
			ExportDate:                  exportAt.Format(time.RFC3339),
			PrometheusURL:               "-",
			PrometheusMode:              "-",
			CPUQueryRange:               rangeValue(cfg.Resources.CPU.Range, cfg.Prometheus.QueryRange),
			MemoryQueryRange:            rangeValue(cfg.Resources.Memory.Range, cfg.Prometheus.QueryRange),
			RateWindow:                  rateWindowValue(cfg),
			CPUAllocatableComparison:    "-",
			MemoryAllocatableComparison: "-",
			TotalNodes:                  "n/a",
			TotalCPU:                    "n/a",
			TotalAllocatableCPU:         "n/a",
			TotalMemory:                 "n/a",
			TotalAllocatableMemory:      "n/a",
			CPURequest:                  emptySuggestionTotal(),
			CPULimit:                    emptySuggestionTotal(),
			MemoryRequest:               emptySuggestionTotal(),
			MemoryLimit:                 emptySuggestionTotal(),
		},
		Suggestion: suggestionRows(cfg),
		Queries:    querySections(cfg),
	}

	containers := cache.Containers()
	report.Summary.TotalPods = countPods(cache)
	report.Summary.TotalContainers = len(containers)
	report.Summary.TotalWorkloads = len(cache.Workload)
	report.Summary.LimitRangeContainers = countLimitRangeContainers(containers)
	applyNodes(&report, cache.Nodes)

	hasPrometheus := client.HasPrometheus(containers)
	report.Summary.PrometheusMode = prometheusMode(cache, cfg, hasPrometheus, metadata.PrometheusLive)
	if hasPrometheus || metadata.PrometheusLive {
		report.Summary.PrometheusURL = sanitizeURL(metadata.PrometheusURL)
		if report.Summary.PrometheusURL == "" && cache.Prometheus != nil {
			report.Summary.PrometheusURL = cache.Prometheus.Host
		}
	}
	if !hasPrometheus {
		report.Summary.CPURequest = emptySuggestionTotal()
		report.Summary.CPULimit = emptySuggestionTotal()
		report.Summary.MemoryRequest = emptySuggestionTotal()
		report.Summary.MemoryLimit = emptySuggestionTotal()
		report.Summary.WorkloadsNeedingAdjustment = 0
		report.Summary.OOMAffectedWorkloadsKube = countOOMKilledWorkloads(containers)
		report.Summary.OOMAffectedContainersProm = nil
		return report
	}

	totals := map[metric]*aggregate{}
	for _, item := range []metric{metricCPURequest, metricCPULimit, metricMemoryRequest, metricMemoryLimit} {
		totals[item] = newAggregate()
	}
	adjustedWorkloads := make(map[string]bool)
	for _, workload := range cache.Workload {
		workloadKey := workload.Namespace + "\x00" + workload.Kind + "\x00" + workload.Name
		groups := make(map[string]*group)
		for _, pod := range workload.Pods {
			for _, container := range pod.Containers {
				key := container.Name
				value := groups[key]
				if value == nil {
					value = &group{name: container.Name}
					groups[key] = value
				}
				value.add(container)
			}
		}
		podCount := float64(len(workload.Pods))
		if podCount == 0 {
			podCount = 1
		}
		for _, value := range groups {
			for _, item := range []metric{metricCPURequest, metricCPULimit, metricMemoryRequest, metricMemoryLimit} {
				current, currentOK := value.current(item)
				suggested, suggestedOK := value.suggestion(item)
				if !suggestedOK {
					continue
				}
				totals[item].add(current, currentOK, suggested, podCount)
				if needsChange(current, currentOK, suggested) {
					totals[item].changes++
					adjustedWorkloads[workloadKey] = true
				}
			}
		}
	}
	report.Summary.WorkloadsNeedingAdjustment = len(adjustedWorkloads)
	report.Summary.OOMAffectedWorkloadsKube = countOOMKilledWorkloads(containers)
	if promOOM, available := countPrometheusOOMContainers(containers); available {
		report.Summary.OOMAffectedContainersProm = &promOOM
	}
	report.Summary.CPURequest = totals[metricCPURequest].suggestionTotal(formatCPU)
	report.Summary.CPULimit = totals[metricCPULimit].suggestionTotal(formatCPU)
	report.Summary.MemoryRequest = totals[metricMemoryRequest].suggestionTotal(formatMemory)
	report.Summary.MemoryLimit = totals[metricMemoryLimit].suggestionTotal(formatMemory)
	report.Summary.CPUAllocatableComparison = compareAllocatable(report.Summary.CPURequest.Suggested, report.Summary.TotalAllocatableCPU)
	report.Summary.MemoryAllocatableComparison = compareAllocatable(report.Summary.MemoryRequest.Suggested, report.Summary.TotalAllocatableMemory)
	return report
}

type group struct {
	name string

	cpuReq      float64
	cpuReqOK    bool
	cpuReqSug   float64
	cpuReqSugOK bool
	cpuLim      float64
	cpuLimOK    bool
	cpuLimSug   float64
	cpuLimSugOK bool
	memReq      float64
	memReqOK    bool
	memReqSug   float64
	memReqSugOK bool
	memLim      float64
	memLimOK    bool
	memLimSug   float64
	memLimSugOK bool
}

func (g *group) add(container model.Container) {
	if quantity, ok := parseQuantity(container.CPUReq); ok {
		g.cpuReq = max(g.cpuReq, quantity)
		g.cpuReqOK = true
	}
	if quantity, ok := parseQuantity(container.CPULim); ok {
		g.cpuLim = max(g.cpuLim, quantity)
		g.cpuLimOK = true
	}
	if quantity, ok := parseQuantity(container.MemReq); ok {
		g.memReq = max(g.memReq, quantity)
		g.memReqOK = true
	}
	if quantity, ok := parseQuantity(container.MemLim); ok {
		g.memLim = max(g.memLim, quantity)
		g.memLimOK = true
	}
	if data := container.Prometheus; data != nil {
		if data.CPURequestSuggestion != nil {
			g.cpuReqSug = max(g.cpuReqSug, *data.CPURequestSuggestion)
			g.cpuReqSugOK = true
		}
		if data.CPULimitSuggestion != nil {
			g.cpuLimSug = max(g.cpuLimSug, *data.CPULimitSuggestion)
			g.cpuLimSugOK = true
		}
		if data.MemoryRequestSuggestion != nil {
			g.memReqSug = max(g.memReqSug, *data.MemoryRequestSuggestion)
			g.memReqSugOK = true
		}
		if data.MemoryLimitSuggestion != nil {
			g.memLimSug = max(g.memLimSug, *data.MemoryLimitSuggestion)
			g.memLimSugOK = true
		}
	}
}

func (g *group) current(item metric) (float64, bool) {
	switch item {
	case metricCPURequest:
		return g.cpuReq, g.cpuReqOK
	case metricCPULimit:
		return g.cpuLim, g.cpuLimOK
	case metricMemoryRequest:
		return g.memReq, g.memReqOK
	default:
		return g.memLim, g.memLimOK
	}
}

func (g *group) suggestion(item metric) (float64, bool) {
	switch item {
	case metricCPURequest:
		return g.cpuReqSug, g.cpuReqSugOK
	case metricCPULimit:
		return g.cpuLimSug, g.cpuLimSugOK
	case metricMemoryRequest:
		return g.memReqSug, g.memReqSugOK
	default:
		return g.memLimSug, g.memLimSugOK
	}
}

type aggregate struct {
	current        float64
	suggested      float64
	missingCurrent int
	changes        int
}

func newAggregate() *aggregate { return &aggregate{} }

func (a *aggregate) add(current float64, currentOK bool, suggested, podCount float64) {
	if !currentOK {
		a.missingCurrent++
	}
	a.current += current * podCount
	a.suggested += suggested * podCount
}

func (a *aggregate) suggestionTotal(format func(float64) string) SuggestionTotal {
	return SuggestionTotal{
		Known:          true,
		Current:        format(a.current),
		Suggested:      format(a.suggested),
		Change:         signed(format, a.current, a.suggested),
		ChangePercent:  percent(a.current, a.suggested),
		MissingCurrent: a.missingCurrent,
		ChangesNeeded:  a.changes,
	}
}

func emptySuggestionTotal() SuggestionTotal {
	return SuggestionTotal{
		Known:         false,
		Current:       "-",
		Suggested:     "-",
		Change:        "-",
		ChangePercent: "-",
	}
}

func needsChange(current float64, currentOK bool, suggested float64) bool {
	if !currentOK {
		return suggested > 0
	}
	if current == 0 {
		return suggested > 0
	}
	return math.Abs(suggested-current)/current > DefaultChangeThreshold
}

func signed(format func(float64) string, current, suggested float64) string {
	change := suggested - current
	if math.Abs(change) < 0.0000001 {
		return "0"
	}
	if change > 0 {
		return "+" + format(change)
	}
	return format(change)
}

func percent(current, suggested float64) string {
	if current == 0 {
		if suggested > 0 {
			return "new"
		}
		return "-"
	}
	return fmt.Sprintf("%+.1f%%", (suggested-current)/current*100)
}

func formatCPU(cores float64) string {
	return fmt.Sprintf("%dm", int(math.Round(cores*1000)))
}

func formatMemory(bytes float64) string {
	return fmt.Sprintf("%dMi", int(math.Round(bytes/(1024*1024))))
}

func parseQuantity(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == "-" {
		return 0, false
	}
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return 0, false
	}
	return quantity.AsApproximateFloat64(), true
}

func applyNodes(report *Report, nodes *client.NodeInventory) {
	if nodes == nil {
		return
	}
	report.Summary.TotalNodes = "n/a"
	if !nodes.Available {
		return
	}
	nodeCount := nodes.TotalNodes
	if nodeCount == 0 && len(nodes.Items) > 0 {
		nodeCount = len(nodes.Items)
	}
	report.Summary.TotalNodes = strconv.Itoa(nodeCount)
	capacityCPU, capacityMemory, allocCPU, allocMemory, cpuOK, memOK, allocCPUOK, allocMemOK := nodeTotals(nodes.Items)
	report.Summary.TotalCPU = nodeValue(nodes.TotalCPUCapacity, capacityCPU, cpuOK, formatCPU)
	report.Summary.TotalAllocatableCPU = nodeValue(nodes.TotalAllocatableCPU, allocCPU, allocCPUOK, formatCPU)
	report.Summary.TotalMemory = nodeValue(nodes.TotalMemoryCapacity, capacityMemory, memOK, formatMemory)
	report.Summary.TotalAllocatableMemory = nodeValue(nodes.TotalAllocatableMemory, allocMemory, allocMemOK, formatMemory)
	for _, item := range nodes.Items {
		report.Nodes = append(report.Nodes, Node{
			Name:              item.Name,
			CapacityCPU:       item.CapacityCPU,
			CapacityMemory:    item.CapacityMemory,
			AllocatableCPU:    item.AllocatableCPU,
			AllocatableMemory: item.AllocatableMemory,
		})
	}
}

func nodeValue(cached string, fallback float64, ok bool, format func(float64) string) string {
	if cached != "" {
		return cached
	}
	if !ok {
		return "n/a"
	}
	return format(fallback)
}

func nodeTotals(nodes []client.CachedNode) (cpu, memory, allocCPU, allocMemory float64, cpuOK, memOK, allocCPUOK, allocMemOK bool) {
	for _, node := range nodes {
		if value, ok := parseQuantity(node.CapacityCPU); ok {
			cpu += value
			cpuOK = true
		}
		if value, ok := parseQuantity(node.CapacityMemory); ok {
			memory += value
			memOK = true
		}
		if value, ok := parseQuantity(node.AllocatableCPU); ok {
			allocCPU += value
			allocCPUOK = true
		}
		if value, ok := parseQuantity(node.AllocatableMemory); ok {
			allocMemory += value
			allocMemOK = true
		}
	}
	return
}

func countPods(cache client.Cache) int {
	count := 0
	for _, workload := range cache.Workload {
		count += len(workload.Pods)
	}
	return count
}

func countOOMKilledWorkloads(containers []model.Container) int {
	workloads := make(map[string]bool)
	for _, container := range containers {
		if container.OOM == "yes" || container.Status != nil && (container.Status.Reason == "OOMKilled" || container.Status.LastTerminatedReason == "OOMKilled") {
			key := container.Namespace + "\x00" + container.Owners + "\x00" + container.OwnerName
			workloads[key] = true
		}
	}
	return len(workloads)
}

// countPrometheusOOMContainers counts distinct containers observed with
// OOMKilled as their last termination reason. The boolean is false when the
// OOM metric was not available, so callers can render unknown instead of zero.
func countPrometheusOOMContainers(containers []model.Container) (int, bool) {
	affected := make(map[string]bool)
	available := false
	for _, container := range containers {
		data := container.Prometheus
		if data == nil {
			continue
		}
		if data.OOMAvailable != nil {
			if !*data.OOMAvailable {
				continue
			}
			available = true
		} else if data.OOMKills != nil {
			// Backward-compatible cache entries lack the availability flag.
			available = true
		} else {
			continue
		}
		if data.OOMKills != nil && *data.OOMKills > 0 {
			affected[container.Namespace+"\x00"+container.Pod+"\x00"+container.Name] = true
		}
	}
	return len(affected), available
}

func prometheusMode(cache client.Cache, cfg config.Config, hasPrometheus, live bool) string {
	if !hasPrometheus {
		return "-"
	}
	if live {
		return "live"
	}
	mode := "cached"
	if cache.Prometheus == nil || cache.Prometheus.Fingerprint != client.PrometheusFingerprint(cfg.Prometheus, cfg.Resources) {
		mode += " stale"
	}
	return mode
}

func sanitizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	return parsed.String()
}

func rangeValue(value config.Duration, fallback time.Duration) string {
	duration := time.Duration(value)
	if duration <= 0 {
		duration = fallback
	}
	return duration.String()
}

func rateWindowValue(cfg config.Config) string {
	cpuRate := cfg.Resources.CPU.RateWindow
	memoryRate := cfg.Resources.Memory.RateWindow
	if cpuRate <= 0 {
		cpuRate = cfg.Prometheus.RateWindow
	}
	if memoryRate <= 0 {
		memoryRate = cfg.Prometheus.RateWindow
	}
	if cpuRate == memoryRate {
		return cpuRate.String()
	}
	return fmt.Sprintf("CPU %s / MEM %s", cpuRate, memoryRate)
}

func countLimitRangeContainers(containers []model.Container) int {
	count := 0
	for _, container := range containers {
		if container.ResourceSource == "LimitRange" {
			count++
		}
	}
	return count
}

func compareAllocatable(suggested, allocatable string) string {
	if suggested == "" || suggested == "-" || allocatable == "" || allocatable == "-" || allocatable == "n/a" {
		return "-"
	}
	suggestedValue, ok := parseQuantity(suggested)
	if !ok {
		return "-"
	}
	allocatableValue, ok := parseQuantity(allocatable)
	if !ok || allocatableValue <= 0 {
		return "-"
	}
	return fmt.Sprintf("%s / %s (%.1f%% of allocatable)", suggested, allocatable, suggestedValue/allocatableValue*100)
}

func suggestionRows(cfg config.Config) []SuggestionRow {
	resourceRow := func(name string, percentile, buffer float64, minimum, unit string) SuggestionRow {
		if percentile <= 0 {
			return SuggestionRow{Resource: name, Formula: "Disabled (percentile 0)"}
		}
		formula := fmt.Sprintf("P%.0f × (1 + %.0f%% buffer)", percentile, buffer)
		if minimum != "" && minimum != "0" && minimum != "0Mi" {
			formula += ", min " + minimum
		}
		formula += ", rounded to " + unit
		return SuggestionRow{Resource: name, Formula: formula}
	}
	return []SuggestionRow{
		{
			Resource: "Prometheus source",
			Formula:  "grouped cAdvisor container CPU, container working-set memory, and kube-state-metrics OOM queries",
		},
		resourceRow("CPU request", cfg.Resources.CPU.Percentiles.Request, cfg.Resources.CPU.SuggestionBufferPercentage.Request, cfg.Resources.CPU.SuggestionMinimum.Request, "1m"),
		resourceRow("CPU limit", cfg.Resources.CPU.Percentiles.Limit, cfg.Resources.CPU.SuggestionBufferPercentage.Limit, cfg.Resources.CPU.SuggestionMinimum.Limit, "1m"),
		resourceRow("Memory request", cfg.Resources.Memory.Percentiles.Request, cfg.Resources.Memory.SuggestionBufferPercentage.Request, cfg.Resources.Memory.SuggestionMinimum.Request, "1Mi"),
		resourceRow("Memory limit", cfg.Resources.Memory.Percentiles.Limit, cfg.Resources.Memory.SuggestionBufferPercentage.Limit, cfg.Resources.Memory.SuggestionMinimum.Limit, "1Mi"),
		{
			Resource: "Aggregation",
			Formula:  "maximum pod sample per owner+container, multiplied by the cached pod count",
		},
		{
			Resource: "Change threshold",
			Formula:  fmt.Sprintf("abs(suggested-current)/current > %.0f%%; missing current + positive suggestion counts as a change", DefaultChangeThreshold*100),
		},
		{
			Resource: "Missing current",
			Formula:  "treated as zero and counted separately",
		},
		{
			Resource: "OOM statistics",
			Formula:  "Primary: max_over_time(kube_pod_container_status_last_terminated_reason{reason=\"OOMKilled\"}) from kube-state-metrics; a gauge observation of last termination reason, not an exact event count. Kubernetes API OOM workload counts use current/last termination status and are kept separate.",
		},
		{
			Resource: "Rate window",
			Formula:  fmt.Sprintf("default %s; CPU %s; memory %s", cfg.Prometheus.RateWindow, cfg.Resources.CPU.RateWindow, cfg.Resources.Memory.RateWindow),
		},
		{
			Resource: "Query step",
			Formula:  fmt.Sprintf("CPU %s; memory auto", cfg.Resources.CPU.QueryStep),
		},
		{
			Resource: "Node totals",
			Formula:  "summed from Kubernetes node capacity and allocatable quantities",
		},
	}
}

func querySections(cfg config.Config) []QuerySection {
	queries := client.PrometheusColumnQueries(cfg.Prometheus, cfg.Resources)
	section := QuerySection{Name: "Prometheus"}
	for _, column := range model.Columns {
		if !strings.HasPrefix(column.ID, "P") {
			continue
		}
		section.Columns = append(section.Columns, ColumnQuery{
			ColumnID: column.ID,
			Header:   column.Header,
			Query:    queries[column.ID],
			Note:     queryNote(column.ID, cfg),
		})
	}
	return []QuerySection{section}
}

func queryNote(columnID string, cfg config.Config) string {
	switch columnID {
	case "P02":
		return fmt.Sprintf("suggestion = value * (1 + %.0f%% buffer), min %s", cfg.Resources.CPU.SuggestionBufferPercentage.Request, cfg.Resources.CPU.SuggestionMinimum.Request)
	case "P04":
		return fmt.Sprintf("suggestion = value * (1 + %.0f%% buffer), min %s", cfg.Resources.CPU.SuggestionBufferPercentage.Limit, cfg.Resources.CPU.SuggestionMinimum.Limit)
	case "P06":
		return fmt.Sprintf("suggestion = value * (1 + %.0f%% buffer), min %s", cfg.Resources.Memory.SuggestionBufferPercentage.Request, cfg.Resources.Memory.SuggestionMinimum.Request)
	case "P08":
		return fmt.Sprintf("suggestion = value * (1 + %.0f%% buffer), min %s", cfg.Resources.Memory.SuggestionBufferPercentage.Limit, cfg.Resources.Memory.SuggestionMinimum.Limit)
	case "P09":
		return "yes when the container's last termination reason was observed as OOMKilled during the interval; not an exact OOM event count"
	default:
		return ""
	}
}
