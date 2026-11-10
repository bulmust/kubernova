package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kubernova/internal/model"
)

func cacheContainers() []model.Container {
	apiOwnership := &model.OwnerReference{
		APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "api-abc", UID: "rs-1", Replicas: "3/3",
		Owner: &model.OwnerReference{
			APIVersion: "apps/v1", Kind: "Deployment", Name: "api", UID: "owner-1", Replicas: "3/3",
		},
	}
	dbOwnership := &model.OwnerReference{
		APIVersion: "apps/v1", Kind: "StatefulSet", Name: "db", UID: "owner-2", Replicas: "1/1",
	}
	return []model.Container{
		{Cluster: "prod", Namespace: "default", Owners: "Deployment", OwnerName: "api", OwnerUID: "owner-1", Pod: "api-1", ContainerUID: "pod-1", Name: "api", Image: "api:v1", Repository: "api", Tag: "v1", Replicas: "3/3", Ownership: apiOwnership},
		{Cluster: "prod", Namespace: "default", Owners: "Deployment", OwnerName: "api", OwnerUID: "owner-1", Pod: "api-1", ContainerUID: "pod-1", Name: "sidecar", Image: "envoy:1.30", Repository: "envoy", Tag: "1.30", Replicas: "3/3", Ownership: apiOwnership},
		{Cluster: "prod", Namespace: "default", Owners: "StatefulSet", OwnerName: "db", OwnerUID: "owner-2", Pod: "db-0", ContainerUID: "pod-2", Name: "db", Image: "postgres:16", Repository: "postgres", Tag: "16", Replicas: "1/1", Ownership: dbOwnership},
	}
}

func TestCachePathIncludesClusterContextAndTimestamp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	at := time.Date(2026, 11, 5, 14, 3, 22, 0, time.UTC)

	first, err := CacheFilePath("prod/cluster", "ctx-a", at)
	if err != nil {
		t.Fatalf("CacheFilePath() error = %v", err)
	}
	second, err := CacheFilePath("prod/cluster", "ctx-b", at)
	if err != nil {
		t.Fatalf("CacheFilePath() error = %v", err)
	}
	if first == second {
		t.Fatalf("cache paths for different contexts are identical: %q", first)
	}
	want := filepath.Join(dir, "kubernova", "clusters", "prod_cluster", "ctx-a", "cache_261105_140322.json")
	if first != want {
		t.Errorf("CacheFilePath() = %q, want %q", first, want)
	}
}

func TestCacheSchemaNestsPodsAndContainersUnderWorkload(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cache := BuildCache("prod", "ctx", cacheContainers(), time.Unix(100, 0))
	if len(cache.Workload) != 2 {
		t.Fatalf("workload = %d, want 2", len(cache.Workload))
	}
	api := cache.Workload[0]
	if api.Namespace != "default" || api.Kind != "Deployment" || api.Name != "api" || len(api.Pods) != 1 {
		t.Errorf("first workload = %#v", api)
	}
	if got := len(api.Pods[0].Containers); got != 2 {
		t.Errorf("api pod containers = %d, want 2", got)
	}
	podMetadata := api.Pods[0]
	if podMetadata.OwnerUID != "owner-1" || podMetadata.Ownership == nil {
		t.Errorf("api pod ownership metadata = %#v", podMetadata)
	}
	if podMetadata.Ownership != nil && (podMetadata.Ownership.Kind != "ReplicaSet" ||
		podMetadata.Ownership.Owner == nil || podMetadata.Ownership.Owner.Kind != "Deployment") {
		t.Errorf("api pod ownership hierarchy = %#v", podMetadata.Ownership)
	}
	if api.Replicas != "3" {
		t.Errorf("api workload replicas = %q, want desired 3", api.Replicas)
	}

	path, err := SaveCache(cache)
	if err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	text := string(raw)
	for _, want := range []string{`"schema_version": 6`, `"workload": [`, `"owner_kind": "Deployment"`, `"replicas": "3"`, `"pods"`, `"containers"`, `"ownership"`, `"kind": "ReplicaSet"`, `"owner": {`} {
		if !strings.Contains(text, want) {
			t.Errorf("cache JSON is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"owners": [`) {
		t.Errorf("cache JSON still uses the owners key:\n%s", text)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode cache JSON: %v", err)
	}
	workloads, ok := decoded["workload"].([]any)
	if !ok || len(workloads) == 0 {
		t.Fatalf("workload JSON = %#v", decoded["workload"])
	}
	workload := workloads[0].(map[string]any)
	if _, ok := workload["owner_uid"]; ok {
		t.Errorf("workload still contains owner_uid: %#v", workload)
	}
	pods := workload["pods"].([]any)
	pod := pods[0].(map[string]any)
	if _, ok := pod["owner_uid"]; !ok {
		t.Errorf("pod is missing owner_uid: %#v", pod)
	}
	ownership, ok := pod["ownership"].(map[string]any)
	if !ok {
		t.Fatalf("pod is missing ownership metadata: %#v", pod)
	}
	if ownership["kind"] != "ReplicaSet" {
		t.Errorf("direct owner kind = %#v, want ReplicaSet", ownership["kind"])
	}
	owner, ok := ownership["owner"].(map[string]any)
	if !ok || owner["kind"] != "Deployment" {
		t.Errorf("ReplicaSet owner = %#v, want Deployment", ownership["owner"])
	}
	for _, forbidden := range []string{"secret", "token", "password"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("cache JSON contains sensitive key %q:\n%s", forbidden, text)
		}
	}

	loaded, loadedPath, err := LoadCache("prod", "ctx")
	if err != nil {
		t.Fatalf("LoadCache() error = %v", err)
	}
	if loadedPath != path {
		t.Errorf("LoadCache() path = %q, want %q", loadedPath, path)
	}
	rows := loaded.Containers()
	if len(rows) != len(cacheContainers()) {
		t.Fatalf("cached containers = %d, want %d", len(rows), len(cacheContainers()))
	}
	if rows[0].Tag != "v1" || rows[0].ContainerUID != "pod-1" || rows[0].Replicas != "3" {
		t.Errorf("cached first row = %#v", rows[0])
	}
}

func TestLoadCacheRejectsWrongIdentity(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cache := BuildCache("prod", "ctx", nil, time.Now())
	if _, err := SaveCache(cache); err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}
	if _, _, err := LoadCache("other", "ctx"); err == nil {
		t.Fatal("LoadCache() error = nil, want identity mismatch")
	}
	if _, _, err := LoadCache("prod", "other"); err == nil {
		t.Fatal("LoadCache() error = nil, want identity mismatch")
	}
}

func TestSaveCacheReplacesAtomically(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	first := BuildCache("prod", "ctx", cacheContainers()[:1], time.Unix(1, 0))
	path, err := SaveCache(first)
	if err != nil {
		t.Fatalf("first SaveCache() error = %v", err)
	}
	second := BuildCache("prod", "ctx", cacheContainers(), time.Unix(2, 0))
	secondPath, err := SaveCache(second)
	if err != nil {
		t.Fatalf("second SaveCache() error = %v", err)
	}
	if secondPath == path {
		t.Fatalf("second cache reused the first filename %q", path)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("older cache %q was not removed", path)
	}

	raw, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var decoded Cache
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("cache is not valid JSON after replacement: %v", err)
	}
	if len(decoded.Containers()) != len(cacheContainers()) {
		t.Errorf("replaced cache containers = %d, want %d", len(decoded.Containers()), len(cacheContainers()))
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("temporary cache file was left behind: %q", entry.Name())
		}
	}
}

func TestLoadCacheMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, _, err := LoadCache("missing", "ctx"); err != ErrCacheNotFound {
		t.Fatalf("LoadCache() error = %v, want ErrCacheNotFound", err)
	}
}

func TestLoadCacheMigratesVersion3(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := CacheFilePath("prod", "ctx", time.Unix(100, 0))
	if err != nil {
		t.Fatalf("CacheFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := `{"schema_version":3,"fetched_at":"1970-01-01T00:00:00Z","cluster":{"cluster":"prod","context":"ctx"},"workload":[]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write v3 cache: %v", err)
	}
	cache, _, err := LoadCache("prod", "ctx")
	if err != nil {
		t.Fatalf("LoadCache() error = %v", err)
	}
	if cache.SchemaVersion != CacheSchemaVersion {
		t.Errorf("migrated version = %d, want %d", cache.SchemaVersion, CacheSchemaVersion)
	}
}

func TestCachePrometheusRoundTripAndCarry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	value := 1.5
	cache := BuildCache("prod", "ctx", []model.Container{{
		Namespace: "default", Owners: "Deployment", OwnerName: "api", Pod: "api-1", Name: "api",
		Prometheus: &model.PrometheusData{Source: "prometheus", CPURequestPercentile: &value, FetchedAt: time.Unix(100, 0)},
	}}, time.Unix(100, 0))
	cache.Prometheus = CachePrometheusFor("https://user:pass@prom.example/path", testPromConfig(), testResources(), time.Unix(100, 0), 24*time.Hour, time.Minute)
	path, err := SaveCache(cache)
	if err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "user") || strings.Contains(text, "pass") || strings.Contains(text, "secret") {
		t.Errorf("cache contains credentials or secrets:\n%s", text)
	}
	if !strings.Contains(text, `"prometheus": {`) || !strings.Contains(text, `"cpu_request_percentile": 1.5`) {
		t.Errorf("cache is missing Prometheus data:\n%s", text)
	}

	loaded, _, err := LoadCache("prod", "ctx")
	if err != nil {
		t.Fatalf("LoadCache() error = %v", err)
	}
	rows := loaded.Containers()
	if len(rows) != 1 || rows[0].Prometheus == nil || rows[0].Prometheus.CPURequestPercentile == nil || *rows[0].Prometheus.CPURequestPercentile != 1.5 {
		t.Errorf("loaded Prometheus data = %#v", rows)
	}
	if loaded.Prometheus == nil || loaded.Prometheus.Host != "prom.example" {
		t.Errorf("cache Prometheus metadata host = %#v", loaded.Prometheus)
	}

	next := []model.Container{{Namespace: "default", Owners: "Deployment", OwnerName: "api", Pod: "api-1", Name: "api"}}
	carried := CarryPrometheus(rows, next)
	if carried[0].Prometheus == nil || !carried[0].Prometheus.FetchedAt.Equal(time.Unix(100, 0)) {
		t.Errorf("Prometheus data was not carried: %#v", carried[0].Prometheus)
	}
	if gone := CarryPrometheus(rows, []model.Container{{Namespace: "default", Owners: "Deployment", OwnerName: "api", Pod: "gone", Name: "api"}}); gone[0].Prometheus != nil {
		t.Error("removed key retained Prometheus data")
	}
}

func TestPrometheusFingerprint(t *testing.T) {
	first := PrometheusFingerprint(testPromConfig(), testResources())
	if first == "" {
		t.Fatal("fingerprint is empty")
	}
	resources := testResources()
	resources.CPU.Percentiles.Request = 80
	if second := PrometheusFingerprint(testPromConfig(), resources); second == first {
		t.Error("fingerprint did not change with percentile")
	}
	labeled := testPromConfig()
	labeled.LabelFilters = map[string]string{"cluster": "prod"}
	if second := PrometheusFingerprint(labeled, testResources()); second == first {
		t.Error("fingerprint did not change with label filters")
	}
}

func TestLoadCacheMigratesVersion4ImageFieldsAndReplicas(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := CacheFilePath("prod", "ctx", time.Unix(100, 0))
	if err != nil {
		t.Fatalf("CacheFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := `{
		"schema_version": 4,
		"fetched_at": "1970-01-01T00:00:00Z",
		"cluster": {"cluster": "prod", "context": "ctx"},
		"workload": [{
			"namespace": "default",
			"owner_kind": "Deployment",
			"owner_name": "etcd",
			"replicas": "2/3",
			"pods": [{
				"name": "etcd-0",
				"uid": "pod",
				"containers": [{
					"cluster": "prod", "namespace": "default", "owner_name": "etcd", "owner_kind": "Deployment",
					"pod": "etcd-0", "name": "etcd", "image": "registry.k8s.io/etcd:3.6.8-0",
					"repository": "registry.k8s.io/etcd", "tag": "3.6.8-0"
				}]
			}]
		}]
	}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write v4 cache: %v", err)
	}
	cache, _, err := LoadCache("prod", "ctx")
	if err != nil {
		t.Fatalf("LoadCache() error = %v", err)
	}
	if cache.SchemaVersion != CacheSchemaVersion {
		t.Errorf("migrated version = %d, want %d", cache.SchemaVersion, CacheSchemaVersion)
	}
	if cache.Workload[0].Replicas != "3" {
		t.Errorf("migrated replicas = %q, want desired 3", cache.Workload[0].Replicas)
	}
	container := cache.Containers()[0]
	if container.Registry != "registry.k8s.io" || container.Repository != "etcd" || container.Tag != "3.6.8-0" {
		t.Errorf("migrated image fields = %#v", container)
	}
}

func TestContainerStatusSurvivesCacheRoundTripWithoutSecrets(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cache := BuildCache("prod", "ctx", []model.Container{{
		Cluster: "prod", Namespace: "default", Owners: "Job", OwnerName: "job", Pod: "job-done", Name: "job",
		Image: "busybox:1.36", Status: &model.ContainerStatus{State: "Terminated", Reason: "Completed", ExitCode: 0},
	}}, time.Unix(100, 0))
	path, err := SaveCache(cache)
	if err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	for _, forbidden := range []string{"secret", "token", "password"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Errorf("cache JSON contains sensitive key %q", forbidden)
		}
	}
	loaded, _, err := LoadCache("prod", "ctx")
	if err != nil {
		t.Fatalf("LoadCache() error = %v", err)
	}
	status := loaded.Containers()[0].Status
	if status == nil || status.State != "Terminated" || status.Reason != "Completed" || status.Running || status.ExitCode != 0 {
		t.Errorf("round-tripped status = %#v", status)
	}
}

func TestCacheNodesRoundTripAndAbsentBlock(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cache := BuildCache("prod", "ctx", cacheContainers()[:1], time.Unix(100, 0))
	cache.Nodes = &NodeInventory{
		Available: true, TotalNodes: 1, TotalCPUCapacity: "4", TotalAllocatableCPU: "3900m",
		TotalMemoryCapacity: "8Gi", TotalAllocatableMemory: "7Gi",
		Items: []CachedNode{{
			Name: "worker-1", CapacityCPU: "4", CapacityMemory: "8Gi", AllocatableCPU: "3900m", AllocatableMemory: "7Gi",
		}},
	}
	path, err := SaveCache(cache)
	if err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}
	loaded, _, err := LoadCache("prod", "ctx")
	if err != nil {
		t.Fatalf("LoadCache() error = %v", err)
	}
	if loaded.Nodes == nil || !loaded.Nodes.Available || len(loaded.Nodes.Items) != 1 ||
		loaded.Nodes.Items[0].AllocatableMemory != "7Gi" || loaded.Nodes.TotalAllocatableMemory != "7Gi" {
		t.Errorf("nodes round trip = %#v", loaded.Nodes)
	}
	rawJSON, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache JSON: %v", err)
	}
	for _, want := range []string{`"total_nodes": 1`, `"total_cpu_capacity": "4"`, `"total_allocatable_cpu": "3900m"`, `"total_memory_capacity": "8Gi"`, `"total_allocatable_memory": "7Gi"`} {
		if !strings.Contains(string(rawJSON), want) {
			t.Errorf("cache JSON missing %q:\n%s", want, rawJSON)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	raw = []byte(strings.Replace(string(raw), `"nodes":`, `"ignored_nodes":`, 1))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	loaded, _, err = LoadCache("prod", "ctx")
	if err != nil {
		t.Fatalf("LoadCache() without nodes error = %v", err)
	}
	if loaded.Nodes != nil {
		t.Errorf("absent nodes block = %#v, want unknown nil", loaded.Nodes)
	}
	for _, forbidden := range []string{"labels", "annotations"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Errorf("node cache contains %q", forbidden)
		}
	}
}
