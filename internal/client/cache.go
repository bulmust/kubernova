package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kubernova/internal/model"
	"kubernova/internal/state"
)

// CacheSchemaVersion is incremented whenever the JSON cache layout changes.
const CacheSchemaVersion = 6

// Cache files are named cache_YYMMDD_HHMMSS.json.
const (
	CacheFilePrefix    = "cache_"
	CacheFileExtension = ".json"
	CacheTimeLayout    = "060102_150405"
	legacyCacheFile    = "cache.json"
)

// CacheFileName returns the timestamped cache filename for a fetch time.
func CacheFileName(at time.Time) string {
	return CacheFilePrefix + at.Format(CacheTimeLayout) + CacheFileExtension
}

// ErrCacheNotFound reports that no usable cache exists for an identity.
var ErrCacheNotFound = errors.New("cache not found")

// CacheIdentity is the cluster plus Kubernetes context a cache belongs to.
type CacheIdentity struct {
	Cluster string `json:"cluster"`
	Context string `json:"context"`
}

// Cache is the persistent, non-secret table snapshot. Workloads are keyed by
// (namespace, owner_kind, owner_name), with pods and containers nested beneath
// them.
type Cache struct {
	SchemaVersion int              `json:"schema_version"`
	FetchedAt     time.Time        `json:"fetched_at"`
	Cluster       CacheIdentity    `json:"cluster"`
	Prometheus    *CachePrometheus `json:"prometheus,omitempty"`
	Nodes         *NodeInventory   `json:"nodes,omitempty"`
	Workload      []CachedWorkload `json:"workload"`
}

// CachedNode is non-secret node capacity metadata. Empty quantities mean the
// API did not provide that value; zero is a valid value.
type CachedNode struct {
	Name              string `json:"name"`
	CapacityCPU       string `json:"capacity_cpu,omitempty"`
	CapacityMemory    string `json:"capacity_memory,omitempty"`
	AllocatableCPU    string `json:"allocatable_cpu,omitempty"`
	AllocatableMemory string `json:"allocatable_memory,omitempty"`
}

// NodeInventory distinguishes an unavailable node list from an empty cluster.
// Aggregate fields are captured with the inventory so exports do not need to
// recompute node totals.
type NodeInventory struct {
	Available              bool         `json:"available"`
	Reason                 string       `json:"reason,omitempty"`
	TotalNodes             int          `json:"total_nodes"`
	TotalCPUCapacity       string       `json:"total_cpu_capacity,omitempty"`
	TotalAllocatableCPU    string       `json:"total_allocatable_cpu,omitempty"`
	TotalMemoryCapacity    string       `json:"total_memory_capacity,omitempty"`
	TotalAllocatableMemory string       `json:"total_allocatable_memory,omitempty"`
	Items                  []CachedNode `json:"items,omitempty"`
}

// CachePrometheus identifies the Prometheus observation stored in a cache.
// Host is sanitized and never contains credentials or query text.
type CachePrometheus struct {
	Source      string    `json:"source"`
	Host        string    `json:"host"`
	Fingerprint string    `json:"fingerprint"`
	FetchedAt   time.Time `json:"fetched_at"`
	Range       string    `json:"range,omitempty"`
	Step        string    `json:"step,omitempty"`
}

// CachedWorkload groups pods by namespace, workload kind, and workload name.
type CachedWorkload struct {
	Namespace string      `json:"namespace"`
	Kind      string      `json:"owner_kind"`
	Name      string      `json:"owner_name"`
	Replicas  string      `json:"replicas,omitempty"`
	Pods      []CachedPod `json:"pods"`
}

// CachedPod groups containers by pod identity. The owner UID and the full
// hierarchical ownership chain (for example pod -> ReplicaSet -> Deployment)
// are metadata on the pod.
type CachedPod struct {
	Name       string                `json:"name"`
	UID        string                `json:"uid"`
	OwnerUID   string                `json:"owner_uid,omitempty"`
	Ownership  *model.OwnerReference `json:"ownership,omitempty"`
	Containers []model.Container     `json:"containers"`
}

// CacheDir returns the directory containing cache files for an identity.
func CacheDir(cluster, context string) (string, error) {
	return state.DirFor(cluster, context)
}

// CacheFilePath returns the timestamped cache path for a cluster, context, and
// fetch time.
func CacheFilePath(cluster, context string, at time.Time) (string, error) {
	dir, err := CacheDir(cluster, context)
	if err != nil {
		return "", err
	}
	if at.IsZero() {
		at = time.Now()
	}
	return filepath.Join(dir, CacheFileName(at.UTC())), nil
}

// BuildCache groups live container rows into the persistent cache schema.
func BuildCache(cluster, context string, containers []model.Container, fetchedAt time.Time) Cache {
	cache := Cache{
		SchemaVersion: CacheSchemaVersion,
		FetchedAt:     fetchedAt.UTC(),
		Cluster:       CacheIdentity{Cluster: cluster, Context: context},
	}
	type ownerKey struct {
		namespace string
		kind      string
		name      string
	}
	ownerIndexes := make(map[ownerKey]int)
	podIndexes := make([]map[string]int, 0)
	for _, container := range containers {
		key := ownerKey{container.Namespace, container.Owners, container.OwnerName}
		index, ok := ownerIndexes[key]
		if !ok {
			index = len(cache.Workload)
			ownerIndexes[key] = index
			cache.Workload = append(cache.Workload, CachedWorkload{
				Namespace: container.Namespace,
				Kind:      container.Owners,
				Name:      container.OwnerName,
				Replicas:  normalizeReplicas(container.Replicas),
			})
			podIndexes = append(podIndexes, make(map[string]int))
		}
		if cache.Workload[index].Replicas == "" {
			cache.Workload[index].Replicas = normalizeReplicas(container.Replicas)
		}
		if podIndexes[index] == nil {
			podIndexes[index] = make(map[string]int)
		}
		podIndex, ok := podIndexes[index][container.Pod]
		if !ok {
			podIndex = len(cache.Workload[index].Pods)
			podIndexes[index][container.Pod] = podIndex
			cache.Workload[index].Pods = append(cache.Workload[index].Pods, CachedPod{
				Name:      container.Pod,
				UID:       container.ContainerUID,
				OwnerUID:  container.OwnerUID,
				Ownership: container.Ownership,
			})
		}
		pod := &cache.Workload[index].Pods[podIndex]
		if pod.UID == "" {
			pod.UID = container.ContainerUID
		}
		if pod.OwnerUID == "" {
			pod.OwnerUID = container.OwnerUID
		}
		if pod.Ownership == nil && container.Ownership != nil {
			pod.Ownership = container.Ownership
		}
		pod.Containers = append(pod.Containers, container)
	}
	return cache
}

// Containers flattens the cache back into table rows in schema order. Pod-level
// owner metadata and workload replicas are restored onto each container because
// those fields are not duplicated in the JSON container objects.
func (c Cache) Containers() []model.Container {
	containers := make([]model.Container, 0)
	for _, workload := range c.Workload {
		for _, pod := range workload.Pods {
			for _, container := range pod.Containers {
				container.OwnerUID = pod.OwnerUID
				container.Replicas = workload.Replicas
				container.Ownership = pod.Ownership
				containers = append(containers, container)
			}
		}
	}
	return containers
}

// backfillImageFields recomputes the parsed image fields for caches written
// before the registry column and the registry-aware parser existed.
func (c *Cache) backfillImageFields() {
	for workloadIndex := range c.Workload {
		c.Workload[workloadIndex].Replicas = normalizeReplicas(c.Workload[workloadIndex].Replicas)
		for podIndex := range c.Workload[workloadIndex].Pods {
			containers := c.Workload[workloadIndex].Pods[podIndex].Containers
			for containerIndex := range containers {
				if strings.TrimSpace(containers[containerIndex].Image) == "" {
					continue
				}
				parsed := model.ParseImage(containers[containerIndex].Image)
				containers[containerIndex].Registry = parsed.Registry
				containers[containerIndex].Repository = parsed.Repository
				containers[containerIndex].Tag = parsed.Tag
			}
		}
	}
}

// normalizeReplicas keeps only the desired count from a legacy ready/desired
// value.
func normalizeReplicas(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if index := strings.LastIndex(value, "/"); index >= 0 {
		value = strings.TrimSpace(value[index+1:])
	}
	if value == "" {
		return "-"
	}
	return value
}

// LoadCache reads and validates the newest cache for an identity.
func LoadCache(cluster, context string) (Cache, string, error) {
	dir, err := CacheDir(cluster, context)
	if err != nil {
		return Cache{}, "", err
	}
	candidates, err := cacheCandidates(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Cache{}, filepath.Join(dir, CacheFileName(time.Now())), ErrCacheNotFound
		}
		return Cache{}, "", err
	}
	if len(candidates) == 0 {
		return Cache{}, filepath.Join(dir, CacheFileName(time.Now())), ErrCacheNotFound
	}

	var lastErr error
	for _, path := range candidates {
		raw, err := os.ReadFile(path)
		if err != nil {
			lastErr = fmt.Errorf("read cache %q: %w", path, err)
			continue
		}
		var cache Cache
		if err := json.Unmarshal(raw, &cache); err != nil {
			lastErr = fmt.Errorf("parse cache %q: %w", path, err)
			continue
		}
		switch cache.SchemaVersion {
		case 3, 4, 5:
			// Older schemas are structurally compatible. Recompute parsed
			// image fields from the complete image reference and keep any
			// existing status metadata. An absent nodes block stays nil
			// (unknown), not an empty known cluster.
			cache.SchemaVersion = CacheSchemaVersion
			cache.backfillImageFields()
		case CacheSchemaVersion:
		default:
			lastErr = fmt.Errorf(
				"cache %q has unsupported schema version %d, want %d or a supported migration",
				path, cache.SchemaVersion, CacheSchemaVersion,
			)
			continue
		}
		if cache.Cluster.Cluster != cluster || cache.Cluster.Context != context {
			lastErr = fmt.Errorf(
				"cache %q belongs to %q/%q, not %q/%q",
				path, cache.Cluster.Cluster, cache.Cluster.Context, cluster, context,
			)
			continue
		}
		return cache, path, nil
	}
	if lastErr != nil {
		return Cache{}, candidates[0], lastErr
	}
	return Cache{}, candidates[0], ErrCacheNotFound
}

// SaveCache atomically writes a timestamped cache for its identity and removes
// older cache files. The temporary file lives in the same directory, so a crash
// cannot expose a partial cache file.
func SaveCache(cache Cache) (string, error) {
	at := cache.FetchedAt
	if at.IsZero() {
		at = time.Now()
	}
	cache.FetchedAt = at.UTC()
	path, err := CacheFilePath(cache.Cluster.Cluster, cache.Cluster.Context, cache.FetchedAt)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return path, fmt.Errorf("create cache directory: %w", err)
	}
	raw, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return path, fmt.Errorf("encode cache: %w", err)
	}
	raw = append(raw, '\n')

	temporary := fmt.Sprintf("%s.%d.tmp", path, time.Now().UnixNano())
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return path, fmt.Errorf("write cache %q: %w", temporary, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return path, fmt.Errorf("replace cache %q: %w", path, err)
	}
	pruneCacheFiles(dir, path)
	return path, nil
}

func cacheCandidates(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		path string
		at   time.Time
	}
	files := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == legacyCacheFile {
			files = append(files, candidate{path: filepath.Join(dir, name)})
			continue
		}
		at, ok := cacheNameTime(name)
		if !ok {
			continue
		}
		files = append(files, candidate{path: filepath.Join(dir, name), at: at})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].at.Equal(files[j].at) {
			return files[i].path > files[j].path
		}
		return files[i].at.After(files[j].at)
	})
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.path)
	}
	return paths, nil
}

func cacheNameTime(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, CacheFilePrefix) || !strings.HasSuffix(name, CacheFileExtension) {
		return time.Time{}, false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, CacheFilePrefix), CacheFileExtension)
	at, err := time.ParseInLocation(CacheTimeLayout, stamp, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

func pruneCacheFiles(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == legacyCacheFile {
			if entry.Name() == legacyCacheFile {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
			continue
		}
		if _, ok := cacheNameTime(entry.Name()); !ok {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if path != keep {
			_ = os.Remove(path)
		}
	}
}
