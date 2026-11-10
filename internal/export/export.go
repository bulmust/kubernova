// Package export renders the unfiltered report and cache data as JSON or
// Markdown and writes it atomically.
package export

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kubernova/internal/client"
	"kubernova/internal/model"
	"kubernova/internal/report"
	"kubernova/internal/state"
)

// Format is an export output format.
type Format string

const (
	FormatJSON     Format = "json"
	FormatMarkdown Format = "md"
	FormatAll      Format = "all"
)

// ParsedFormat validates a user-supplied format name.
func ParsedFormat(value string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(FormatAll):
		return FormatAll, nil
	case string(FormatJSON):
		return FormatJSON, nil
	case string(FormatMarkdown), "markdown":
		return FormatMarkdown, nil
	default:
		return "", fmt.Errorf("unknown export format %q (use json, md, or all)", value)
	}
}

// Dir returns the default exports directory beside state.yaml.
func Dir(cluster, context string) (string, error) {
	dir, err := state.DirFor(cluster, context)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "exports"), nil
}

// FileName returns the default export filename for an identity.
func FileName(cluster, context string, at time.Time, extension string) string {
	if at.IsZero() {
		at = time.Now()
	}
	return fmt.Sprintf("kubernova-%s-%s.%s", identity(cluster, context), at.Format("20060102-150405"), extension)
}

func identity(cluster, context string) string {
	value := strings.TrimSpace(cluster) + "-" + strings.TrimSpace(context)
	value = strings.Trim(value, "-.")
	if value == "" {
		return "context"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Meta describes the export source and schema.
type Meta struct {
	SchemaVersion int               `json:"schema_version"`
	GeneratedAt   string            `json:"generated_at"`
	Cached        bool              `json:"cached"`
	Sources       map[string]string `json:"sources"`
	Columns       []ColumnMeta      `json:"columns"`
}

// ColumnMeta records a stable table column.
type ColumnMeta struct {
	ID     string `json:"id"`
	Header string `json:"header"`
	Key    string `json:"key"`
}

type document struct {
	Summary    report.Summary         `json:"summary"`
	Suggestion []report.SuggestionRow `json:"suggestion"`
	Queries    []report.QuerySection  `json:"queries"`
	Meta       Meta                   `json:"meta"`
	Nodes      []report.Node          `json:"nodes"`
	Workloads  []workload             `json:"workloads"`
}

type workload struct {
	Namespace string `json:"namespace"`
	OwnerKind string `json:"owner_kind"`
	OwnerName string `json:"owner_name"`
	Replicas  string `json:"replicas"`
	Pods      []pod  `json:"pods"`
}

type pod struct {
	Name       string                `json:"name"`
	UID        string                `json:"uid"`
	OwnerUID   string                `json:"owner_uid,omitempty"`
	Ownership  *model.OwnerReference `json:"ownership,omitempty"`
	Containers []container           `json:"containers"`
}

type container struct {
	Pod        string                 `json:"pod"`
	Name       string                 `json:"name"`
	Columns    map[string]string      `json:"columns"`
	Status     *model.ContainerStatus `json:"status,omitempty"`
	Prometheus *model.PrometheusData  `json:"prometheus,omitempty"`
}

// RenderJSON renders the complete unfiltered export.
func RenderJSON(value report.Report, cache client.Cache, cached bool, generatedAt time.Time) ([]byte, error) {
	if generatedAt.IsZero() {
		generatedAt = time.Now()
	}
	doc := document{
		Summary:    value.Summary,
		Suggestion: value.Suggestion,
		Queries:    value.Queries,
		Meta: Meta{
			SchemaVersion: cache.SchemaVersion,
			GeneratedAt:   generatedAt.Format(time.RFC3339),
			Cached:        cached,
			Sources:       sources(value, cached),
			Columns:       columnMetadata(),
		},
		Nodes: value.Nodes,
	}
	if doc.Nodes == nil {
		doc.Nodes = []report.Node{}
	}
	for _, item := range cache.Workload {
		entry := workload{
			Namespace: item.Namespace,
			OwnerKind: item.Kind,
			OwnerName: item.Name,
			Replicas:  item.Replicas,
		}
		for _, cachedPod := range item.Pods {
			podEntry := pod{
				Name:      cachedPod.Name,
				UID:       cachedPod.UID,
				OwnerUID:  cachedPod.OwnerUID,
				Ownership: cachedPod.Ownership,
			}
			for _, cachedContainer := range cachedPod.Containers {
				podEntry.Containers = append(podEntry.Containers, container{
					Pod:        cachedContainer.Pod,
					Name:       cachedContainer.Name,
					Columns:    columnValues(cachedContainer),
					Status:     cachedContainer.Status,
					Prometheus: cachedContainer.Prometheus,
				})
			}
			if podEntry.Containers == nil {
				podEntry.Containers = []container{}
			}
			entry.Pods = append(entry.Pods, podEntry)
		}
		if entry.Pods == nil {
			entry.Pods = []pod{}
		}
		doc.Workloads = append(doc.Workloads, entry)
	}
	if doc.Workloads == nil {
		doc.Workloads = []workload{}
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// RenderMarkdown renders the summary and every column in the supplied sort
// order. The caller passes all rows; filters and namespace selection are not
// applied here.
func RenderMarkdown(value report.Report, containers []model.Container, cached bool, _ time.Time) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Kubernova report: %s / %s\n\n", value.Summary.Cluster, value.Summary.Context)
	if cached {
		b.WriteString("_Source: cached table snapshot; Kubernetes API was not used._\n\n")
	}
	b.WriteString("## Summary\n\n| Field | Value |\n| --- | --- |\n")
	for _, row := range summaryRows(value.Summary) {
		fmt.Fprintf(&b, "| %s | %s |\n", escape(row[0]), escape(row[1]))
	}
	b.WriteString("\n## Suggestion\n\n")
	b.WriteString("| Resource | Formula |\n| --- | --- |\n")
	for _, row := range value.Suggestion {
		fmt.Fprintf(&b, "| %s | %s |\n", escape(row.Resource), escape(row.Formula))
	}
	b.WriteString("\n## Queries\n\n")
	for _, section := range value.Queries {
		fmt.Fprintf(&b, "### %s\n\n", escape(section.Name))
		for _, column := range section.Columns {
			fmt.Fprintf(&b, "**%s %s**\n\n", column.ColumnID, escape(column.Header))
			b.WriteString("```promql\n")
			b.WriteString(column.Query)
			b.WriteString("\n```\n")
			if column.Note != "" {
				fmt.Fprintf(&b, "\n%s\n", escape(column.Note))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("## Containers\n\n")
	if len(containers) == 0 {
		b.WriteString("_No containers._\n")
		return []byte(b.String())
	}
	b.WriteString("|")
	for _, column := range model.Columns {
		fmt.Fprintf(&b, " %s (%s) |", escape(column.Header), column.ID)
	}
	b.WriteString("\n|")
	for range model.Columns {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	for _, item := range containers {
		values := item.Row()
		b.WriteString("|")
		for index := range model.Columns {
			fmt.Fprintf(&b, " %s |", escape(values[index]))
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func summaryRows(summary report.Summary) [][2]string {
	rows := [][2]string{
		{"Cluster", summary.Cluster},
		{"Context", summary.Context},
		{"Prometheus URL", summary.PrometheusURL},
	}
	if summary.CPUQueryRange == summary.MemoryQueryRange {
		rows = append(rows, [2]string{"Query interval", summary.CPUQueryRange})
	} else {
		rows = append(rows,
			[2]string{"CPU query range", summary.CPUQueryRange},
			[2]string{"MEM query range", summary.MemoryQueryRange},
		)
	}
	rows = append(rows,
		[2]string{"Rate window", summary.RateWindow},
		[2]string{"Export date", summary.ExportDate},
		[2]string{"Total nodes", summary.TotalNodes},
		[2]string{"Total CPU (capacity cores)", summary.TotalCPU},
		[2]string{"Total allocatable CPU cores", summary.TotalAllocatableCPU},
		[2]string{"Total memory (capacity)", summary.TotalMemory},
		[2]string{"Total allocatable memory", summary.TotalAllocatableMemory},
		[2]string{"Total pods", fmt.Sprint(summary.TotalPods)},
		[2]string{"Total containers", fmt.Sprint(summary.TotalContainers)},
		[2]string{"Total workloads", fmt.Sprint(summary.TotalWorkloads)},
		[2]string{"Containers using LimitRange resources", fmt.Sprint(summary.LimitRangeContainers)},
		[2]string{"Workloads needing adjustment", fmt.Sprint(summary.WorkloadsNeedingAdjustment)},
		[2]string{"OOM-affected containers (Prometheus)", intPointer(summary.OOMAffectedContainersProm)},
		[2]string{"OOM-affected workloads (Kubernetes API)", fmt.Sprint(summary.OOMAffectedWorkloadsKube)},
		[2]string{"Total CPU Req->Sug", arrow(summary.CPURequest)},
		[2]string{"Total CPU Request Change Suggestion", change(summary.CPURequest)},
		[2]string{"Total CPU Lim->Sug", arrow(summary.CPULimit)},
		[2]string{"Total CPU Limit Change Suggestion", change(summary.CPULimit)},
		[2]string{"Total MEM Req->Sug", arrow(summary.MemoryRequest)},
		[2]string{"Total MEM Request Change Suggestion", change(summary.MemoryRequest)},
		[2]string{"Total MEM Lim->Sug", arrow(summary.MemoryLimit)},
		[2]string{"Total MEM Limit Change Suggestion", change(summary.MemoryLimit)},
		[2]string{"CPU request suggestion vs allocatable CPU", summary.CPUAllocatableComparison},
		[2]string{"Memory request suggestion vs allocatable memory", summary.MemoryAllocatableComparison},
		[2]string{"Number of changes needed: CPU request", fmt.Sprint(summary.CPURequest.ChangesNeeded)},
		[2]string{"Number of changes needed: CPU limit", fmt.Sprint(summary.CPULimit.ChangesNeeded)},
		[2]string{"Number of changes needed: MEM request", fmt.Sprint(summary.MemoryRequest.ChangesNeeded)},
		[2]string{"Number of changes needed: MEM limit", fmt.Sprint(summary.MemoryLimit.ChangesNeeded)},
		[2]string{"Missing current values: CPU request", fmt.Sprint(summary.CPURequest.MissingCurrent)},
		[2]string{"Missing current values: CPU limit", fmt.Sprint(summary.CPULimit.MissingCurrent)},
		[2]string{"Missing current values: MEM request", fmt.Sprint(summary.MemoryRequest.MissingCurrent)},
		[2]string{"Missing current values: MEM limit", fmt.Sprint(summary.MemoryLimit.MissingCurrent)},
	)
	return rows
}

func intPointer(value *int) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprint(*value)
}

func arrow(value report.SuggestionTotal) string {
	if !value.Known {
		return "-"
	}
	return value.Current + " -> " + value.Suggested
}

func change(value report.SuggestionTotal) string {
	if !value.Known {
		return "-"
	}
	if value.ChangePercent == "-" || value.ChangePercent == "" {
		return value.Change
	}
	return value.Change + " (" + value.ChangePercent + ")"
}

func sources(value report.Report, cached bool) map[string]string {
	source := "kube"
	if cached {
		source = "cached"
	}
	return map[string]string{
		"summary":    source,
		"nodes":      source,
		"workloads":  source,
		"prometheus": value.Summary.PrometheusMode,
	}
}

func columnMetadata() []ColumnMeta {
	columns := make([]ColumnMeta, 0, len(model.Columns))
	for _, column := range model.Columns {
		columns = append(columns, ColumnMeta{ID: column.ID, Header: column.Header, Key: columnKey(column)})
	}
	return columns
}

func columnValues(value model.Container) map[string]string {
	values := value.Row()
	result := make(map[string]string, len(model.Columns))
	for index, column := range model.Columns {
		result[columnKey(column)] = values[index]
	}
	return result
}

func columnKey(column model.Column) string {
	key := strings.ToLower(strings.TrimSpace(column.Header))
	key = strings.ReplaceAll(key, " ", "_")
	key = strings.ReplaceAll(key, "-", "_")
	return key
}

func escape(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	return value
}

// WriteAtomic writes data through a temporary file in the destination
// directory and renames it into place.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create export directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".export-*")
	if err != nil {
		return fmt.Errorf("create export temporary file: %w", err)
	}
	name := temporary.Name()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		_ = os.Remove(name)
		return fmt.Errorf("write export: %w", err)
	}
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		_ = os.Remove(name)
		return fmt.Errorf("set export permissions: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("close export: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("replace export %q: %w", path, err)
	}
	return nil
}
