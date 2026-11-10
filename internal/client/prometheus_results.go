package client

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"kubernova/internal/model"
)

// ExecuteQueryPlan runs the plan's grouped queries with bounded concurrency.
func ExecuteQueryPlan(ctx context.Context, prometheusClient *PrometheusClient, plan ResourceQueryPlan, maxConcurrent int) (QueryResults, error) {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	type task struct {
		result *PrometheusResult
		query  string
		oom    bool
	}
	var results QueryResults
	tasks := make([]task, 0, 3)
	if plan.CPU != nil {
		tasks = append(tasks, task{result: &results.CPU, query: plan.CPU.Query})
	}
	if plan.Memory != nil {
		tasks = append(tasks, task{result: &results.Memory, query: plan.Memory.Query})
	}
	if plan.OOM != nil {
		tasks = append(tasks, task{result: &results.OOM, query: plan.OOM.Query, oom: true})
	}
	if len(tasks) == 0 {
		return results, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	semaphore := make(chan struct{}, maxConcurrent)
	errs := make(chan error, len(tasks))
	var wait sync.WaitGroup
	for _, item := range tasks {
		item := item
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() {
				if value := recover(); value != nil {
					errs <- fmt.Errorf("prometheus query panicked: %v", value)
					cancel()
				}
			}()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			result, err := prometheusClient.Query(ctx, item.query)
			if err != nil {
				var promErr *PrometheusError
				if errors.As(err, &promErr) && promErr.Kind == PrometheusEmptyResult {
					// An empty OOM result cannot be distinguished from a
					// missing kube-state-metrics series, so report it as
					// unavailable rather than silently assuming zero.
					return
				}
				errs <- err
				cancel()
				return
			}
			*item.result = result
			if item.oom {
				results.OOMAvailable = true
			}
		}()
	}
	go func() {
		wait.Wait()
		close(errs)
	}()
	for err := range errs {
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

// QueryResults bundles the three possible grouped instant-query results.
type QueryResults struct {
	CPU    PrometheusResult
	Memory PrometheusResult
	OOM    PrometheusResult
	// OOMAvailable is true when the OOM query completed successfully, even
	// with an empty result. It distinguishes a valid zero from unavailable
	// kube-state-metrics data.
	OOMAvailable bool
}

type resourceKey struct {
	namespace string
	pod       string
	container string
}

type ownerContainerKey struct {
	namespace string
	ownerKind string
	ownerName string
	container string
}

type prometheusObservation struct {
	cpuRequest *float64
	cpuLimit   *float64
	memRequest *float64
	memLimit   *float64
	oom        *int64
}

// ApplyPrometheusResults matches Prometheus series to Kubernetes rows, applies
// owner-level max aggregation for percentiles and sum aggregation for OOM
// counts, and computes suggestions. Series absent from Kubernetes data are
// ignored.
func ApplyPrometheusResults(containers []model.Container, plan ResourceQueryPlan, results QueryResults, fetchedAt time.Time) []model.Container {
	if len(containers) == 0 {
		return containers
	}
	byResource := make(map[resourceKey]int, len(containers))
	owners := make(map[ownerContainerKey][]int)
	for index, container := range containers {
		if excludedContainer(container.Name) {
			continue
		}
		byResource[resourceKey{container.Namespace, container.Pod, container.Name}] = index
		key := ownerContainerKey{container.Namespace, container.Owners, container.OwnerName, container.Name}
		owners[key] = append(owners[key], index)
	}

	observations := make(map[ownerContainerKey]*prometheusObservation)
	get := func(key ownerContainerKey) *prometheusObservation {
		value := observations[key]
		if value == nil {
			value = &prometheusObservation{}
			observations[key] = value
		}
		return value
	}
	ownerKeyForSample := func(sample PrometheusSample) (ownerContainerKey, bool) {
		index, ok := byResource[resourceKey{
			sample.Metric["namespace"], sample.Metric["pod"], sample.Metric["container"],
		}]
		if !ok {
			return ownerContainerKey{}, false
		}
		container := containers[index]
		return ownerContainerKey{container.Namespace, container.Owners, container.OwnerName, container.Name}, true
	}
	maxFloat := func(target **float64, value float64) {
		if *target == nil || value > **target {
			copy := value
			*target = &copy
		}
	}

	if plan.CPU != nil {
		for _, sample := range results.CPU.Result {
			request, limit := percentileParts(plan.CPU, sample)
			if !request && !limit {
				continue
			}
			key, ok := ownerKeyForSample(sample)
			if !ok {
				continue
			}
			value := get(key)
			if request {
				maxFloat(&value.cpuRequest, sample.Value)
			}
			if limit {
				maxFloat(&value.cpuLimit, sample.Value)
			}
		}
	}
	if plan.Memory != nil {
		for _, sample := range results.Memory.Result {
			request, limit := percentileParts(plan.Memory, sample)
			if !request && !limit {
				continue
			}
			key, ok := ownerKeyForSample(sample)
			if !ok {
				continue
			}
			value := get(key)
			if request {
				maxFloat(&value.memRequest, sample.Value)
			}
			if limit {
				maxFloat(&value.memLimit, sample.Value)
			}
		}
	}
	if plan.OOM != nil {
		for _, sample := range results.OOM.Result {
			if sample.Value <= 0 {
				continue
			}
			key, ok := ownerKeyForSample(sample)
			if !ok {
				continue
			}
			value := get(key)
			if value.oom == nil {
				value.oom = new(int64)
			}
			// The query uses max_over_time on a gauge. Presence means the
			// container was observed with OOMKilled as its last termination
			// reason; it is not an event count.
			*value.oom = 1
		}
	}

	for key, value := range observations {
		for _, index := range owners[key] {
			container := &containers[index]
			if container.Prometheus == nil {
				container.Prometheus = &model.PrometheusData{Source: "prometheus"}
			}
			data := container.Prometheus
			data.Source = "prometheus"
			data.FetchedAt = fetchedAt
			data.Stale = false
			if plan.CPU != nil {
				data.Range = promDuration(plan.CPU.Range)
				data.Step = promDuration(plan.CPU.Step)
				if value.cpuRequest != nil {
					copy := *value.cpuRequest
					data.CPURequestPercentile = &copy
					suggestion := math.Max(*value.cpuRequest*(1+plan.CPU.RequestBuffer/100), plan.CPU.RequestMinimum)
					data.CPURequestSuggestion = &suggestion
				}
				if value.cpuLimit != nil {
					copy := *value.cpuLimit
					data.CPULimitPercentile = &copy
					suggestion := math.Max(*value.cpuLimit*(1+plan.CPU.LimitBuffer/100), plan.CPU.LimitMinimum)
					data.CPULimitSuggestion = &suggestion
				}
			}
			if plan.Memory != nil {
				if plan.CPU == nil {
					data.Range = promDuration(plan.Memory.Range)
					data.Step = promDuration(plan.Memory.Step)
				}
				if value.memRequest != nil {
					copy := *value.memRequest
					data.MemoryRequestPercentile = &copy
					suggestion := math.Max(*value.memRequest*(1+plan.Memory.RequestBuffer/100), plan.Memory.RequestMinimum)
					data.MemoryRequestSuggestion = &suggestion
				}
				if value.memLimit != nil {
					copy := *value.memLimit
					data.MemoryLimitPercentile = &copy
					suggestion := math.Max(*value.memLimit*(1+plan.Memory.LimitBuffer/100), plan.Memory.LimitMinimum)
					data.MemoryLimitSuggestion = &suggestion
				}
			}
			if plan.OOM != nil {
				available := results.OOMAvailable || len(results.OOM.Result) > 0
				copy := available
				data.OOMAvailable = &copy
				if value.oom != nil {
					count := *value.oom
					data.OOMKills = &count
				}
			}
		}
	}
	return containers
}

func percentileParts(group *PercentileQuery, sample PrometheusSample) (request, limit bool) {
	if group.RequestPercentile > 0 && group.LimitPercentile > 0 && group.RequestPercentile == group.LimitPercentile {
		return true, true
	}
	tag := sample.Metric["quantile"]
	if group.RequestPercentile > 0 && group.LimitPercentile > 0 {
		switch tag {
		case "request":
			return true, false
		case "limit":
			return false, true
		default:
			return false, false
		}
	}
	if group.RequestPercentile > 0 {
		return true, false
	}
	if group.LimitPercentile > 0 {
		return false, true
	}
	return false, false
}
