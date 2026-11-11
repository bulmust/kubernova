package client

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"kubernova/internal/config"
	"kubernova/internal/model"
)

// PrometheusHost returns the sanitized host for an endpoint.
func PrometheusHost(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Host
}

// PrometheusFingerprint covers the query-affecting settings. Cached values with
// a different fingerprint are shown as stale rather than discarded.
func PrometheusFingerprint(prometheus config.Prometheus, resources config.Resources) string {
	labels := make([]string, 0, len(prometheus.LabelFilters))
	for key, value := range prometheus.LabelFilters {
		labels = append(labels, key+"="+value)
	}
	sort.Strings(labels)
	parts := []string{
		"query_range=" + prometheus.QueryRange.String(),
		"prom_rate=" + prometheus.RateWindow.String(),
		"cpu_enabled=" + strconv.FormatBool(resources.CPU.Enabled),
		"cpu_range=" + time.Duration(resources.CPU.Range).String(),
		"cpu_req=" + floatString(resources.CPU.Percentiles.Request),
		"cpu_lim=" + floatString(resources.CPU.Percentiles.Limit),
		"cpu_buf_req=" + floatString(resources.CPU.SuggestionBufferPercentage.Request),
		"cpu_buf_lim=" + floatString(resources.CPU.SuggestionBufferPercentage.Limit),
		"cpu_min_req=" + resources.CPU.SuggestionMinimum.Request,
		"cpu_min_lim=" + resources.CPU.SuggestionMinimum.Limit,
		"cpu_rate=" + resources.CPU.RateWindow.String(),
		"cpu_step=" + resources.CPU.QueryStep,
		"mem_enabled=" + strconv.FormatBool(resources.Memory.Enabled),
		"mem_range=" + time.Duration(resources.Memory.Range).String(),
		"mem_rate=" + resources.Memory.RateWindow.String(),
		"mem_oom=" + strconv.FormatBool(resources.Memory.OOMKilledCheck),
		"mem_oom_mult=" + floatString(resources.Memory.OOMKilledLimitMultiplier),
		"mem_req=" + floatString(resources.Memory.Percentiles.Request),
		"mem_lim=" + floatString(resources.Memory.Percentiles.Limit),
		"mem_buf_req=" + floatString(resources.Memory.SuggestionBufferPercentage.Request),
		"mem_buf_lim=" + floatString(resources.Memory.SuggestionBufferPercentage.Limit),
		"mem_min_req=" + resources.Memory.SuggestionMinimum.Request,
		"mem_min_lim=" + resources.Memory.SuggestionMinimum.Limit,
		"labels=" + strings.Join(labels, ","),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:8])
}

func floatString(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// CarryPrometheus copies previous Prometheus observations to surviving rows.
// Keys that disappear from the Kubernetes refresh are dropped.
func CarryPrometheus(previous, next []model.Container) []model.Container {
	type key struct {
		namespace string
		ownerKind string
		ownerName string
		pod       string
		container string
	}
	cached := make(map[key]*model.PrometheusData)
	for _, container := range previous {
		if container.Prometheus == nil {
			continue
		}
		cached[key{container.Namespace, container.Owners, container.OwnerName, container.Pod, container.Name}] = container.Prometheus
	}
	for index := range next {
		value := cached[key{next[index].Namespace, next[index].Owners, next[index].OwnerName, next[index].Pod, next[index].Name}]
		if value != nil {
			next[index].Prometheus = value
		}
	}
	return next
}

// HasPrometheus reports whether any row has Prometheus observations.
func HasPrometheus(containers []model.Container) bool {
	for _, container := range containers {
		if container.Prometheus != nil {
			return true
		}
	}
	return false
}

// MarkPrometheusStale marks observations from a different fingerprint.
func MarkPrometheusStale(containers []model.Container, stale bool) {
	for index := range containers {
		if containers[index].Prometheus != nil {
			containers[index].Prometheus.Stale = stale
		}
	}
}

// CachePrometheusFor builds cache metadata for the current configuration.
func CachePrometheusFor(endpoint string, prometheus config.Prometheus, resources config.Resources, fetchedAt time.Time, rangeValue, step time.Duration) *CachePrometheus {
	return &CachePrometheus{
		Source:      "prometheus",
		Host:        PrometheusHost(endpoint),
		Fingerprint: PrometheusFingerprint(prometheus, resources),
		FetchedAt:   fetchedAt.UTC(),
		Range:       promDuration(rangeValue),
		Step:        promDuration(step),
	}
}
