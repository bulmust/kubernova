package client

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"kubernova/internal/config"
	"kubernova/internal/model"
)

// ResourceQueryPlan is the bounded set of instant queries for one refresh.
type ResourceQueryPlan struct {
	CPU    *PercentileQuery
	Memory *PercentileQuery
	OOM    *OOMQuery
}

// PercentileQuery is one grouped percentile query.
type PercentileQuery struct {
	Query             string
	Range             time.Duration
	Step              time.Duration
	RequestPercentile float64
	LimitPercentile   float64
	RequestBuffer     float64
	LimitBuffer       float64
	RequestMinimum    float64
	LimitMinimum      float64
}

// OOMQuery is one grouped query for containers whose last termination reason
// was observed as OOMKilled. It is a gauge observation, not an exact event
// count.
type OOMQuery struct {
	Query string
	Range time.Duration
}

// PrometheusNamespaceBatchSize is the maximum number of namespaces included in
// one PromQL query. Keeping batches small avoids server-side query timeouts on
// clusters with many namespaces.
const PrometheusNamespaceBatchSize = 5

// BuildQueryPlan constructs pure PromQL for enabled resource groups.
func BuildQueryPlan(prometheus config.Prometheus, resources config.Resources, containers []model.Container) ResourceQueryPlan {
	return BuildQueryPlanForNamespaces(prometheus, resources, prometheusNamespaces(containers))
}

// BuildQueryPlanForNamespaces constructs pure PromQL for the given namespaces.
func BuildQueryPlanForNamespaces(prometheus config.Prometheus, resources config.Resources, namespaces []string) ResourceQueryPlan {
	if !prometheus.Enabled || len(namespaces) == 0 {
		return ResourceQueryPlan{}
	}
	selector := prometheusSelector(namespaces, prometheus.LabelFilters)
	var plan ResourceQueryPlan

	if resources.CPU.Enabled && (resources.CPU.Percentiles.Request > 0 || resources.CPU.Percentiles.Limit > 0) {
		rangeValue := resources.CPU.Range
		if rangeValue == 0 {
			rangeValue = config.Duration(prometheus.QueryRange)
		}
		rangeDuration := time.Duration(rangeValue)
		step := queryStep(resources.CPU.QueryStep, rangeDuration)
		expression := fmt.Sprintf(
			"sum by (namespace, pod, container) (rate(container_cpu_usage_seconds_total{%s}[%s]))",
			selector,
			promDuration(resources.CPU.RateWindow),
		)
		rangeExpression := fmt.Sprintf("(%s)[%s:%s]", expression, promDuration(rangeDuration), promDuration(step))
		plan.CPU = &PercentileQuery{
			Query:             percentileExpression(rangeExpression, resources.CPU.Percentiles.Request, resources.CPU.Percentiles.Limit),
			Range:             rangeDuration,
			Step:              step,
			RequestPercentile: resources.CPU.Percentiles.Request,
			LimitPercentile:   resources.CPU.Percentiles.Limit,
			RequestBuffer:     resources.CPU.SuggestionBufferPercentage.Request,
			LimitBuffer:       resources.CPU.SuggestionBufferPercentage.Limit,
			RequestMinimum:    cpuMinimum(resources.CPU.SuggestionMinimum.Request),
			LimitMinimum:      cpuMinimum(resources.CPU.SuggestionMinimum.Limit),
		}
	}

	if resources.Memory.Enabled {
		rangeValue := resources.Memory.Range
		if rangeValue == 0 {
			rangeValue = config.Duration(prometheus.QueryRange)
		}
		rangeDuration := time.Duration(rangeValue)
		step := queryStep("auto", rangeDuration)
		if resources.Memory.Percentiles.Request > 0 || resources.Memory.Percentiles.Limit > 0 {
			expression := fmt.Sprintf(
				"sum by (namespace, pod, container) (container_memory_working_set_bytes{%s})",
				selector,
			)
			rangeExpression := fmt.Sprintf("(%s)[%s:%s]", expression, promDuration(rangeDuration), promDuration(step))
			plan.Memory = &PercentileQuery{
				Query:             percentileExpression(rangeExpression, resources.Memory.Percentiles.Request, resources.Memory.Percentiles.Limit),
				Range:             rangeDuration,
				Step:              step,
				RequestPercentile: resources.Memory.Percentiles.Request,
				LimitPercentile:   resources.Memory.Percentiles.Limit,
				RequestBuffer:     resources.Memory.SuggestionBufferPercentage.Request,
				LimitBuffer:       resources.Memory.SuggestionBufferPercentage.Limit,
				RequestMinimum:    memoryMinimum(resources.Memory.SuggestionMinimum.Request),
				LimitMinimum:      memoryMinimum(resources.Memory.SuggestionMinimum.Limit),
			}
		}
		if resources.Memory.OOMKilledCheck {
			plan.OOM = &OOMQuery{
				Query: fmt.Sprintf(
					"max_over_time(kube_pod_container_status_last_terminated_reason{%s}[%s])",
					oomSelector(namespaces),
					promDuration(rangeDuration),
				),
				Range: rangeDuration,
			}
		}
	}
	return plan
}

// PrometheusNamespaces returns the distinct namespaces that have containers
// eligible for Prometheus analysis, sorted for stable batching.
func PrometheusNamespaces(containers []model.Container) []string {
	return prometheusNamespaces(containers)
}

// PrometheusNamespaceBatches splits namespaces into batches of at most size.
func PrometheusNamespaceBatches(namespaces []string, size int) [][]string {
	if size <= 0 {
		size = PrometheusNamespaceBatchSize
	}
	if len(namespaces) == 0 {
		return nil
	}
	batches := make([][]string, 0, (len(namespaces)+size-1)/size)
	for start := 0; start < len(namespaces); start += size {
		end := start + size
		if end > len(namespaces) {
			end = len(namespaces)
		}
		batches = append(batches, append([]string(nil), namespaces[start:end]...))
	}
	return batches
}

func prometheusNamespaces(containers []model.Container) []string {
	seen := make(map[string]bool)
	namespaces := make([]string, 0)
	for _, container := range containers {
		if container.Namespace == "" || excludedContainer(container.Name) {
			continue
		}
		if seen[container.Namespace] {
			continue
		}
		seen[container.Namespace] = true
		namespaces = append(namespaces, container.Namespace)
	}
	sort.Strings(namespaces)
	return namespaces
}

func excludedContainer(name string) bool {
	return strings.TrimSpace(name) == "" || strings.EqualFold(name, "POD") || strings.EqualFold(name, "pause")
}

func prometheusSelector(namespaces []string, filters map[string]string) string {
	parts := []string{
		`image!=""`,
		`container!=""`,
		`container!="POD"`,
		`container!="pause"`,
	}
	if len(namespaces) > 0 {
		quoted := make([]string, 0, len(namespaces))
		for _, namespace := range namespaces {
			quoted = append(quoted, regexpQuote(namespace))
		}
		parts = append(parts, fmt.Sprintf(`namespace=~"%s"`, strings.Join(quoted, "|")))
	}
	keys := make([]string, 0, len(filters))
	for key := range filters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, key, regexpQuote(filters[key])))
	}
	return strings.Join(parts, ",")
}

// oomSelector matches only labels that exist on the kube-state-metrics
// kube_pod_container_status_last_terminated_reason metric. In particular, it
// never uses image or cAdvisor-only label filters.
func oomSelector(namespaces []string) string {
	parts := []string{
		`reason="OOMKilled"`,
		`container!=""`,
		`container!="POD"`,
		`container!="pause"`,
	}
	if len(namespaces) > 0 {
		quoted := make([]string, 0, len(namespaces))
		for _, namespace := range namespaces {
			quoted = append(quoted, regexpQuote(namespace))
		}
		parts = append(parts, fmt.Sprintf(`namespace=~"%s"`, strings.Join(quoted, "|")))
	}
	return strings.Join(parts, ",")
}

func percentileExpression(expression string, request, limit float64) string {
	switch {
	case request > 0 && limit > 0 && request == limit:
		return fmt.Sprintf("quantile_over_time(%s, %s)", percentileValue(request), expression)
	case request > 0 && limit > 0:
		return fmt.Sprintf(
			`label_replace(quantile_over_time(%s, %s), "quantile", "request", "container", ".+") or label_replace(quantile_over_time(%s, %s), "quantile", "limit", "container", ".+")`,
			percentileValue(request), expression,
			percentileValue(limit), expression,
		)
	case request > 0:
		return fmt.Sprintf("quantile_over_time(%s, %s)", percentileValue(request), expression)
	case limit > 0:
		return fmt.Sprintf("quantile_over_time(%s, %s)", percentileValue(limit), expression)
	default:
		return ""
	}
}

func percentileValue(percentile float64) string {
	return strconv.FormatFloat(percentile/100, 'f', -1, 64)
}

func queryStep(configured string, rangeValue time.Duration) time.Duration {
	if configured != "" && configured != "auto" {
		if step, err := time.ParseDuration(configured); err == nil && step > 0 {
			return step
		}
	}
	step := rangeValue / 360
	if step < 15*time.Second {
		step = rangeValue / 10
	}
	if step < time.Second {
		step = time.Second
	}
	if step > 5*time.Minute {
		step = 5 * time.Minute
	}
	return step.Truncate(time.Second)
}

func promDuration(value time.Duration) string {
	if value <= 0 {
		return "0s"
	}
	seconds := int64(value / time.Second)
	if seconds%3600 == 0 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}

func cpuMinimum(value string) float64 {
	quantity, err := resource.ParseQuantity(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return quantity.AsApproximateFloat64()
}

func memoryMinimum(value string) float64 {
	quantity, err := resource.ParseQuantity(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return float64(quantity.Value())
}

func regexpQuote(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return replacer.Replace(value)
}

// PrometheusColumnQueries returns the PromQL used for each Prometheus-backed
// table column. Disabled columns return "-". Suggestion columns share the
// usage query and apply their buffer and minimum afterwards.
func PrometheusColumnQueries(prometheus config.Prometheus, resources config.Resources) map[string]string {
	queries := map[string]string{
		"P01": "-", "P02": "-", "P03": "-", "P04": "-",
		"P05": "-", "P06": "-", "P07": "-", "P08": "-", "P09": "-",
	}
	if !prometheus.Enabled {
		return queries
	}
	selector := prometheusSelector([]string{"<namespaces>"}, prometheus.LabelFilters)

	if resources.CPU.Enabled {
		rangeValue := resources.CPU.Range
		if rangeValue == 0 {
			rangeValue = config.Duration(prometheus.QueryRange)
		}
		rangeDuration := time.Duration(rangeValue)
		step := queryStep(resources.CPU.QueryStep, rangeDuration)
		expression := fmt.Sprintf(
			"sum by (namespace, pod, container) (rate(container_cpu_usage_seconds_total{%s}[%s]))",
			selector,
			promDuration(resources.CPU.RateWindow),
		)
		rangeExpression := fmt.Sprintf("(%s)[%s:%s]", expression, promDuration(rangeDuration), promDuration(step))
		if resources.CPU.Percentiles.Request > 0 {
			query := percentileExpression(rangeExpression, resources.CPU.Percentiles.Request, 0)
			queries["P01"], queries["P02"] = query, query
		}
		if resources.CPU.Percentiles.Limit > 0 {
			query := percentileExpression(rangeExpression, 0, resources.CPU.Percentiles.Limit)
			queries["P03"], queries["P04"] = query, query
		}
	}

	if resources.Memory.Enabled {
		rangeValue := resources.Memory.Range
		if rangeValue == 0 {
			rangeValue = config.Duration(prometheus.QueryRange)
		}
		rangeDuration := time.Duration(rangeValue)
		step := queryStep("auto", rangeDuration)
		expression := fmt.Sprintf(
			"sum by (namespace, pod, container) (container_memory_working_set_bytes{%s})",
			selector,
		)
		rangeExpression := fmt.Sprintf("(%s)[%s:%s]", expression, promDuration(rangeDuration), promDuration(step))
		if resources.Memory.Percentiles.Request > 0 {
			query := percentileExpression(rangeExpression, resources.Memory.Percentiles.Request, 0)
			queries["P05"], queries["P06"] = query, query
		}
		if resources.Memory.Percentiles.Limit > 0 {
			query := percentileExpression(rangeExpression, 0, resources.Memory.Percentiles.Limit)
			queries["P07"], queries["P08"] = query, query
		}
		if resources.Memory.OOMKilledCheck {
			queries["P09"] = fmt.Sprintf(
				"max_over_time(kube_pod_container_status_last_terminated_reason{%s}[%s])",
				oomSelector([]string{"<namespaces>"}),
				promDuration(rangeDuration),
			)
		}
	}
	return queries
}
