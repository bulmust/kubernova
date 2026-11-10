package model

import (
	"fmt"
	"math"
	"time"
)

// Column is one table column with a stable identity independent of its display
// position. IDs such as K01 remain valid when columns are hidden or reordered.
type Column struct {
	ID              string
	Header          string
	Description     string
	HiddenByDefault bool
}

// Columns are the stable table columns in display order. Descriptions are the
// single source of truth for the column picker and any help surface.
var Columns = []Column{
	{ID: "K02", Header: "NAMESPACE", Description: "pod namespace"},
	{ID: "K04", Header: "OWNER KIND", Description: "topmost workload owner kind"},
	{ID: "K03", Header: "OWNER NAME", Description: "topmost workload owner name"},
	{ID: "K05", Header: "POD", Description: "pod name"},
	{ID: "K08", Header: "CONTAINER", Description: "container name"},
	{ID: "K19", Header: "STATUS", Description: "container state and reason"},
	{ID: "K09", Header: "CPU REQ", Description: "CPU request quantity"},
	{ID: "K10", Header: "CPU LIM", Description: "CPU limit quantity"},
	{ID: "K11", Header: "MEM REQ", Description: "memory request quantity"},
	{ID: "K12", Header: "MEM LIM", Description: "memory limit quantity"},
	{ID: "K13", Header: "OOM", Description: "whether the container was ever OOMKilled"},
	{ID: "K17", Header: "REPLICAS", Description: "desired replica count"},
	{ID: "K16", Header: "TAG", Description: "image tag, or - when missing"},
	{ID: "K01", Header: "CLUSTER", Description: "Kubernetes cluster name", HiddenByDefault: true},
	{ID: "K06", Header: "IMAGE", Description: "full container image reference", HiddenByDefault: true},
	{ID: "K18", Header: "REGISTRY", Description: "image registry host", HiddenByDefault: true},
	{ID: "K07", Header: "RESOURCE SRC", Description: "where resource requests and limits came from", HiddenByDefault: true},
	{ID: "K14", Header: "CONTAINER UID", Description: "pod UID for the container", HiddenByDefault: true},
	{ID: "K15", Header: "OWNER UID", Description: "topmost owner UID", HiddenByDefault: true},
	{ID: "P01", Header: "CPU_REQ_USE", Description: "CPU request usage percentile", HiddenByDefault: true},
	{ID: "P02", Header: "CPU_REQ_SUG", Description: "CPU request suggestion", HiddenByDefault: true},
	{ID: "P03", Header: "CPU_LIM_USE", Description: "CPU limit usage percentile", HiddenByDefault: true},
	{ID: "P04", Header: "CPU_LIM_SUG", Description: "CPU limit suggestion", HiddenByDefault: true},
	{ID: "P05", Header: "MEM_REQ_USE", Description: "memory request usage percentile", HiddenByDefault: true},
	{ID: "P06", Header: "MEM_REQ_SUG", Description: "memory request suggestion", HiddenByDefault: true},
	{ID: "P07", Header: "MEM_LIM_USE", Description: "memory limit usage percentile", HiddenByDefault: true},
	{ID: "P08", Header: "MEM_LIM_SUG", Description: "memory limit suggestion", HiddenByDefault: true},
	{ID: "P09", Header: "OOM_AFFECTED", Description: "container observed with OOMKilled as last termination reason", HiddenByDefault: true},
}

// Headers are the table column headers in display order.
var Headers = columnHeaders()

func columnHeaders() []string {
	headers := make([]string, len(Columns))
	for index, column := range Columns {
		headers[index] = column.Header
	}
	return headers
}

// OwnerReference is one link in a pod's ownership chain. Owner points at the
// next owner up the hierarchy, for example a ReplicaSet's owner Deployment.
type OwnerReference struct {
	APIVersion string          `json:"api_version,omitempty"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	UID        string          `json:"uid,omitempty"`
	Replicas   string          `json:"replicas,omitempty"`
	Owner      *OwnerReference `json:"owner,omitempty"`
}

// PrometheusData contains Prometheus-derived observations for one container.
// Pointers distinguish missing values from legitimate zero usage.
type PrometheusData struct {
	Source                  string    `json:"source"`
	CPURequestPercentile    *float64  `json:"cpu_request_percentile,omitempty"`
	CPURequestSuggestion    *float64  `json:"cpu_request_suggestion,omitempty"`
	CPULimitPercentile      *float64  `json:"cpu_limit_percentile,omitempty"`
	CPULimitSuggestion      *float64  `json:"cpu_limit_suggestion,omitempty"`
	MemoryRequestPercentile *float64  `json:"memory_request_percentile,omitempty"`
	MemoryRequestSuggestion *float64  `json:"memory_request_suggestion,omitempty"`
	MemoryLimitPercentile   *float64  `json:"memory_limit_percentile,omitempty"`
	MemoryLimitSuggestion   *float64  `json:"memory_limit_suggestion,omitempty"`
	OOMKills                *int64    `json:"oom_kills,omitempty"`
	OOMAvailable            *bool     `json:"oom_available,omitempty"`
	FetchedAt               time.Time `json:"fetched_at"`
	Range                   string    `json:"range,omitempty"`
	Step                    string    `json:"step,omitempty"`
	Stale                   bool      `json:"stale,omitempty"`
}

// ContainerStatus is the non-secret subset of a Kubernetes container status
// that is persisted and rendered in the table.
type ContainerStatus struct {
	State                string `json:"state"`
	Reason               string `json:"reason"`
	ExitCode             int32  `json:"exit_code"`
	Running              bool   `json:"running"`
	LastTerminatedReason string `json:"last_terminated_reason,omitempty"`
	RestartCount         int32  `json:"restart_count,omitempty"`
}

// Text renders the state, including a reason when Kubernetes supplied one.
func (s ContainerStatus) Text() string {
	switch s.State {
	case "Running":
		if s.Reason != "" {
			return "Running/" + s.Reason
		}
		return "Running"
	case "Waiting":
		if s.Reason != "" {
			return "Waiting/" + s.Reason
		}
		return "Waiting"
	case "Terminated":
		if s.Reason != "" {
			return s.Reason
		}
		return "Terminated"
	default:
		if s.Reason != "" {
			return s.Reason
		}
		return s.State
	}
}

// Container is a table row built from a pod container.
type Container struct {
	Cluster        string           `json:"cluster"`
	Namespace      string           `json:"namespace"`
	OwnerName      string           `json:"owner_name"`
	Owners         string           `json:"owner_kind"`
	Pod            string           `json:"pod"`
	ContainerUID   string           `json:"container_uid"`
	OwnerUID       string           `json:"-"`
	Image          string           `json:"image"`
	Registry       string           `json:"registry"`
	Repository     string           `json:"repository"`
	Tag            string           `json:"tag"`
	ResourceSource string           `json:"resource_source"`
	Name           string           `json:"name"`
	CPUReq         string           `json:"cpu_req"`
	CPULim         string           `json:"cpu_lim"`
	MemReq         string           `json:"mem_req"`
	MemLim         string           `json:"mem_lim"`
	OOM            string           `json:"oom"`
	Status         *ContainerStatus `json:"status,omitempty"`
	Replicas       string           `json:"-"`
	Ownership      *OwnerReference  `json:"-"`
	Prometheus     *PrometheusData  `json:"prometheus,omitempty"`
}

// StatusText renders the status cell, or "-" when no status is available.
func (c Container) StatusText() string {
	if c.Status == nil {
		return "-"
	}
	text := c.Status.Text()
	if text == "" {
		return "-"
	}
	return text
}

// Completed reports whether the container terminated successfully.
func (c Container) Completed() bool {
	return c.Status != nil && c.Status.State == "Terminated" && c.Status.Reason == "Completed"
}

// Row renders a container as a table row. The order matches Columns.
func (c Container) Row() []string {
	cpuRequestUsage := formatCPU(c.prometheusField(func(data *PrometheusData) *float64 { return data.CPURequestPercentile }))
	cpuRequestSuggestion := formatSuggestion(c.CPUReq, c.prometheusField(func(data *PrometheusData) *float64 { return data.CPURequestSuggestion }), formatCPU)
	cpuLimitUsage := formatCPU(c.prometheusField(func(data *PrometheusData) *float64 { return data.CPULimitPercentile }))
	cpuLimitSuggestion := formatSuggestion(c.CPULim, c.prometheusField(func(data *PrometheusData) *float64 { return data.CPULimitSuggestion }), formatCPU)
	memoryRequestUsage := formatMemory(c.prometheusField(func(data *PrometheusData) *float64 { return data.MemoryRequestPercentile }))
	memoryRequestSuggestion := formatSuggestion(c.MemReq, c.prometheusField(func(data *PrometheusData) *float64 { return data.MemoryRequestSuggestion }), formatMemory)
	memoryLimitUsage := formatMemory(c.prometheusField(func(data *PrometheusData) *float64 { return data.MemoryLimitPercentile }))
	memoryLimitSuggestion := formatSuggestion(c.MemLim, c.prometheusField(func(data *PrometheusData) *float64 { return data.MemoryLimitSuggestion }), formatMemory)
	oomKills := "-"
	if c.Prometheus != nil {
		available := c.Prometheus.OOMAvailable == nil || c.Prometheus.OOMAvailable != nil && *c.Prometheus.OOMAvailable
		if c.Prometheus.OOMKills != nil {
			if *c.Prometheus.OOMKills > 0 {
				oomKills = "yes"
			} else if available {
				oomKills = "no"
			}
		} else if available && c.Prometheus.OOMAvailable != nil {
			oomKills = "no"
		}
	}
	if c.Prometheus != nil && c.Prometheus.Stale {
		cpuRequestUsage = staleMarker(cpuRequestUsage)
		cpuLimitUsage = staleMarker(cpuLimitUsage)
		memoryRequestUsage = staleMarker(memoryRequestUsage)
		memoryLimitUsage = staleMarker(memoryLimitUsage)
		oomKills = staleMarker(oomKills)
	}
	// The order must match Columns; TestRowMatchesColumns enforces it.
	return []string{
		c.Namespace,
		c.Owners,
		c.OwnerName,
		c.Pod,
		c.Name,
		c.StatusText(),
		c.CPUReq,
		c.CPULim,
		c.MemReq,
		c.MemLim,
		c.OOM,
		c.Replicas,
		c.Tag,
		c.Cluster,
		c.Image,
		c.Registry,
		c.ResourceSource,
		c.ContainerUID,
		c.OwnerUID,
		cpuRequestUsage,
		cpuRequestSuggestion,
		cpuLimitUsage,
		cpuLimitSuggestion,
		memoryRequestUsage,
		memoryRequestSuggestion,
		memoryLimitUsage,
		memoryLimitSuggestion,
		oomKills,
	}
}

func (c Container) prometheusField(field func(*PrometheusData) *float64) *float64 {
	if c.Prometheus == nil {
		return nil
	}
	return field(c.Prometheus)
}

func formatCPU(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%dm", int(math.Round(*value*1000)))
}

func formatMemory(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%dMi", int(math.Round(*value/(1024*1024))))
}

func formatSuggestion(current string, suggestion *float64, format func(*float64) string) string {
	if suggestion == nil {
		return "-"
	}
	next := format(suggestion)
	if current == "" || current == "-" {
		return next
	}
	return current + " -> " + next
}

func staleMarker(value string) string {
	if value == "-" {
		return value
	}
	return value + "~"
}
