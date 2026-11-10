package model

import (
	"strings"
	"testing"
)

func TestParseImage(t *testing.T) {
	tests := []struct {
		name       string
		reference  string
		registry   string
		repository string
		tag        string
	}{
		{"tagged", "nginx:1.27", "docker.io", "nginx", "1.27"},
		{"untagged", "nginx", "docker.io", "nginx", "-"},
		{"registry and tag", "registry.example.com:5000/team/app:1.2", "registry.example.com:5000", "team/app", "1.2"},
		{"registry without tag", "registry.example.com:5000/team/app", "registry.example.com:5000", "team/app", "-"},
		{"kubernetes registry", "registry.k8s.io/etcd:3.6.8-0", "registry.k8s.io", "etcd", "3.6.8-0"},
		{"nested repository", "registry.k8s.io/coredns/coredns:v1.14.2", "registry.k8s.io", "coredns/coredns", "v1.14.2"},
		{"localhost port", "localhost:5000/app:1.2", "localhost:5000", "app", "1.2"},
		{"unqualified path", "library/nginx:1", "docker.io", "library/nginx", "1"},
		{"tag and digest", "nginx:1.25@sha256:abcdef", "docker.io", "nginx", "1.25"},
		{"digest only", "nginx@sha256:abcdef", "docker.io", "nginx", "-"},
		{"registry tag and digest", "registry:5000/app:2.0@sha256:abcdef", "registry:5000", "app", "2.0"},
		{"empty", "", "-", "-", "-"},
		{"missing", "-", "-", "-", "-"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := ParseImage(test.reference)
			if parsed.Registry != test.registry || parsed.Repository != test.repository || parsed.Tag != test.tag {
				t.Errorf("ParseImage(%q) = %#v, want %s/%s/%s", test.reference, parsed, test.registry, test.repository, test.tag)
			}
		})
	}
}

func TestColumnIDsAreStable(t *testing.T) {
	seen := make(map[string]bool)
	for index, column := range Columns {
		if column.ID == "" || seen[column.ID] {
			t.Fatalf("column %d has a missing or duplicate ID %q", index, column.ID)
		}
		seen[column.ID] = true
		if column.Description == "" {
			t.Errorf("column %s has no description", column.ID)
		}
		if Headers[index] != column.Header {
			t.Errorf("Headers[%d] = %q, want %q", index, Headers[index], column.Header)
		}
	}
	if got := cellByID(t, Container{}, "K04"); got != "" {
		t.Errorf("empty container K04 = %q", got)
	}
	header := ""
	for _, column := range Columns {
		if column.ID == "K04" {
			header = column.Header
		}
	}
	if header != "OWNER KIND" {
		t.Errorf("K04 header = %q, want OWNER KIND", header)
	}
}

func cellByID(t *testing.T, container Container, id string) string {
	t.Helper()
	row := container.Row()
	for index, column := range Columns {
		if column.ID == id {
			return row[index]
		}
	}
	t.Fatalf("column %s not found", id)
	return ""
}

// TestRowMatchesColumns guards the Columns and Row orderings, which must be
// kept in lockstep by hand.
func TestRowMatchesColumns(t *testing.T) {
	container := Container{
		Cluster: "cluster", Namespace: "ns", OwnerName: "owner-name", Owners: "Deployment", Pod: "pod",
		Image: "image", ResourceSource: "LimitRange", Name: "container",
		CPUReq: "1m", CPULim: "2m", MemReq: "3Mi", MemLim: "4Mi", OOM: "no",
		ContainerUID: "pod-uid", OwnerUID: "owner-uid", Tag: "tag", Replicas: "3", Registry: "registry",
		Status: &ContainerStatus{State: "Running", Running: true},
	}
	if got := len(container.Row()); got != len(Columns) {
		t.Fatalf("Row() has %d cells, want %d (one per column)", got, len(Columns))
	}
	want := map[string]string{
		"K01": "cluster", "K02": "ns", "K03": "owner-name", "K04": "Deployment", "K05": "pod",
		"K06": "image", "K07": "LimitRange", "K08": "container", "K09": "1m", "K10": "2m",
		"K11": "3Mi", "K12": "4Mi", "K13": "no", "K14": "pod-uid", "K15": "owner-uid",
		"K16": "tag", "K17": "3", "K18": "registry", "K19": "Running",
	}
	for id, value := range want {
		if got := cellByID(t, container, id); got != value {
			t.Errorf("%s = %q, want %q", id, got, value)
		}
	}
	for _, column := range Columns {
		if _, ok := want[column.ID]; !ok && column.ID[0] == 'K' {
			t.Errorf("column %s is not covered by this test", column.ID)
		}
	}
}

func TestDefaultVisibleColumns(t *testing.T) {
	var visible []string
	for _, column := range Columns {
		if !column.HiddenByDefault {
			visible = append(visible, column.ID)
		}
	}
	want := "K02 K04 K03 K05 K08 K19 K09 K10 K11 K12 K13 K17 K16"
	if got := strings.Join(visible, " "); got != want {
		t.Errorf("default visible columns = %s, want %s", got, want)
	}
}

func TestContainerStatusText(t *testing.T) {
	tests := map[ContainerStatus]string{
		{State: "Running", Running: true}:                   "Running",
		{State: "Waiting", Reason: "CrashLoopBackOff"}:      "Waiting/CrashLoopBackOff",
		{State: "Terminated", Reason: "Completed"}:          "Completed",
		{State: "Terminated", Reason: "OOMKilled"}:          "OOMKilled",
		{State: "Terminated", Reason: "Error", ExitCode: 1}: "Error",
	}
	for status, want := range tests {
		if got := status.Text(); got != want {
			t.Errorf("ContainerStatus.Text() = %q, want %q", got, want)
		}
	}
}

func TestPrometheusSuggestionsShowCurrentAndSuggestedValues(t *testing.T) {
	cpuRequest := 0.15
	cpuLimit := 0.5
	memoryRequest := 100.0 * 1024 * 1024
	memoryLimit := 150.0 * 1024 * 1024
	container := Container{
		CPUReq: "100m", CPULim: "400m", MemReq: "64Mi", MemLim: "100Mi",
		Prometheus: &PrometheusData{
			Source:                  "prometheus",
			CPURequestSuggestion:    &cpuRequest,
			CPULimitSuggestion:      &cpuLimit,
			MemoryRequestSuggestion: &memoryRequest,
			MemoryLimitSuggestion:   &memoryLimit,
		},
	}
	want := map[string]string{
		"P02": "100m -> 150m",
		"P04": "400m -> 500m",
		"P06": "64Mi -> 100Mi",
		"P08": "100Mi -> 150Mi",
	}
	for id, value := range want {
		if got := cellByID(t, container, id); got != value {
			t.Errorf("suggestion cell %s = %q, want %q", id, got, value)
		}
	}
	container.Prometheus.Stale = true
	if got := cellByID(t, container, "P08"); got != "100Mi -> 150Mi" {
		t.Errorf("stale suggestion cell = %q, want no stale marker", got)
	}
}

func TestPrometheusValuesFormatAndStaleMarker(t *testing.T) {
	cpu := 0.1234
	mem := 64.0 * 1024 * 1024
	oom := int64(3)
	container := Container{Prometheus: &PrometheusData{
		Source: "prometheus", CPURequestPercentile: &cpu, MemoryRequestPercentile: &mem, OOMKills: &oom,
	}}
	if cellByID(t, container, "P01") != "123m" || cellByID(t, container, "P05") != "64Mi" || cellByID(t, container, "P09") != "yes" {
		t.Errorf("Prometheus row = %v", container.Row())
	}
	container.Prometheus.Stale = true
	if cellByID(t, container, "P01") != "123m~" || cellByID(t, container, "P09") != "yes~" {
		t.Errorf("stale Prometheus row = %v", container.Row())
	}
}
