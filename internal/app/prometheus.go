package app

import (
	"kubernova/internal/client"
	"kubernova/internal/config"
	"kubernova/internal/state"
	"net/url"
	"strings"
	"time"
)

func availablePrometheusColumns(cfg config.Config) []string {
	if !cfg.Prometheus.Enabled {
		return nil
	}
	columns := make([]string, 0, 9)
	cpu := cfg.Resources.CPU
	if cpu.Enabled && cpu.Percentiles.Request > 0 {
		columns = append(columns, "P01", "P02")
	}
	if cpu.Enabled && cpu.Percentiles.Limit > 0 {
		columns = append(columns, "P03", "P04")
	}
	memory := cfg.Resources.Memory
	if memory.Enabled && memory.Percentiles.Request > 0 {
		columns = append(columns, "P05", "P06")
	}
	if memory.Enabled && memory.Percentiles.Limit > 0 {
		columns = append(columns, "P07", "P08")
	}
	if memory.Enabled && memory.OOMKilledCheck {
		columns = append(columns, "P09")
	}
	return columns
}

func prometheusOptions(cfg config.Config, endpoint string) client.PrometheusOptions {
	return client.PrometheusOptions{
		Endpoint:          endpoint,
		SSLVerify:         cfg.Prometheus.SSLVerify,
		ConnectionTimeout: cfg.Prometheus.ConnectionTimeout,
		RequestTimeout:    cfg.Prometheus.RequestTimeout,
		TenantHeader:      cfg.Prometheus.TenantHeader,
		BearerTokenEnv:    cfg.Prometheus.BearerTokenEnv,
		BasicUsernameEnv:  cfg.Prometheus.BasicUsernameEnv,
		BasicPasswordEnv:  cfg.Prometheus.BasicPasswordEnv,
	}
}

func planRangeStep(plan client.ResourceQueryPlan, fallback time.Duration) (time.Duration, time.Duration) {
	switch {
	case plan.CPU != nil:
		return plan.CPU.Range, plan.CPU.Step
	case plan.Memory != nil:
		return plan.Memory.Range, plan.Memory.Step
	default:
		return fallback, 0
	}
}

func sanitizePrometheusURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	return parsed.String()
}

func copyLabelFilters(filters map[string]string) map[string]string {
	if len(filters) == 0 {
		return nil
	}
	copied := make(map[string]string, len(filters))
	for key, value := range filters {
		copied[key] = value
	}
	return copied
}

// migrateLegacyPrometheus copies moved global settings into a context state
// without overwriting values the user already stored for that context.
func migrateLegacyPrometheus(value *state.State, legacy *config.PrometheusContext) bool {
	if value == nil || legacy == nil {
		return false
	}
	migrated := false
	if value.PrometheusURL == "" && legacy.URL != "" {
		value.PrometheusURL = sanitizePrometheusURL(legacy.URL)
		migrated = migrated || value.PrometheusURL != ""
	}
	if value.PrometheusSSLVerify == nil && legacy.SSLVerify != nil {
		sslVerify := *legacy.SSLVerify
		value.PrometheusSSLVerify = &sslVerify
		migrated = true
	}
	if value.PrometheusTenantHeader == "" && legacy.TenantHeader != "" {
		value.PrometheusTenantHeader = legacy.TenantHeader
		migrated = true
	}
	if value.PrometheusBearerTokenEnv == "" && legacy.BearerTokenEnv != "" {
		value.PrometheusBearerTokenEnv = legacy.BearerTokenEnv
		migrated = true
	}
	if value.PrometheusBasicUsernameEnv == "" && legacy.BasicUsernameEnv != "" {
		value.PrometheusBasicUsernameEnv = legacy.BasicUsernameEnv
		migrated = true
	}
	if value.PrometheusBasicPasswordEnv == "" && legacy.BasicPasswordEnv != "" {
		value.PrometheusBasicPasswordEnv = legacy.BasicPasswordEnv
		migrated = true
	}
	if len(value.PrometheusLabelFilters) == 0 && len(legacy.LabelFilters) > 0 {
		value.PrometheusLabelFilters = copyLabelFilters(legacy.LabelFilters)
		migrated = true
	}
	return migrated
}

type prometheusDecision int

const (
	prometheusSkip prometheusDecision = iota
	prometheusPrompt
	prometheusConnect
)

// prometheusAutoDecision centralizes the startup/refresh/context rules. A
// filled Prometheus cache always wins over automatic network activity.
func prometheusAutoDecision(enabled, cached, suppressed bool, endpoint string) prometheusDecision {
	if !enabled || cached {
		return prometheusSkip
	}
	if strings.TrimSpace(endpoint) == "" {
		if suppressed {
			return prometheusSkip
		}
		return prometheusPrompt
	}
	return prometheusConnect
}

func columnSource(columnID string) string {
	if strings.HasPrefix(columnID, "P") {
		return "prometheus"
	}
	return "kubeapi"
}
