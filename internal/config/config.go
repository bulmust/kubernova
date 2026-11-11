// Package config manages the embedded defaults, the XDG configuration files,
// effective configuration merging, validation, and backups.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	"k8s.io/apimachinery/pkg/api/resource"

	configdefaults "kubernova/config/kubernova"
	"kubernova/internal/registry"
)

// DefaultYAML is the documentation-rich default configuration generated from
// the definitions under config/kubernova/.
var DefaultYAML = configdefaults.YAML()

// Config is the effective Kubernova configuration after defaults and the user
// override have been merged.
type Config struct {
	Keybindings map[string]string `yaml:"keybindings"`
	View        View              `yaml:"view"`
	Prometheus  Prometheus        `yaml:"prometheus"`
	Resources   Resources         `yaml:"resources"`
	Kubernetes  Kubernetes        `yaml:"kubernetes"`
	Skin        Skin              `yaml:"skin"`
}

// View contains default table view settings. Per-cluster state overrides these
// defaults and is the only place where user view mutations are persisted.
type View struct {
	SortColumn     string `yaml:"sort_column"`
	SortDescending bool   `yaml:"sort_descending"`
	Fullscreen     bool   `yaml:"fullscreen"`
}

// Prometheus groups Prometheus connection and query settings. The cluster-
// dependent fields below (URL, TLS, tenant, auth environment names, and label
// filters) are runtime overlays supplied by per-context state; global
// configuration only defines enabled, timeouts, query range, and concurrency.
// Authentication values are environment-variable names, never secret values.
type Prometheus struct {
	Enabled           bool              `yaml:"enabled"`
	URL               string            `yaml:"url"`
	SSLVerify         bool              `yaml:"ssl_verify"`
	ConnectionTimeout time.Duration     `yaml:"connection_timeout"`
	RequestTimeout    time.Duration     `yaml:"request_timeout"`
	QueryRange        time.Duration     `yaml:"query_range"`
	RateWindow        time.Duration     `yaml:"rate_window"`
	TenantHeader      string            `yaml:"tenant_header"`
	BearerTokenEnv    string            `yaml:"bearer_token_env"`
	BasicUsernameEnv  string            `yaml:"basic_username_env"`
	BasicPasswordEnv  string            `yaml:"basic_password_env"`
	LabelFilters      map[string]string `yaml:"label_filters"`
	MaxConcurrent     int               `yaml:"max_concurrent_queries"`
}

// Resources groups CPU and memory analysis settings.
type Resources struct {
	CPU    ResourceCPU    `yaml:"cpu"`
	Memory ResourceMemory `yaml:"memory"`
}

type ResourceCPU struct {
	Enabled                    bool                `yaml:"enabled"`
	Range                      Duration            `yaml:"range"`
	Percentiles                ResourcePercentiles `yaml:"percentiles"`
	SuggestionBufferPercentage Percentages         `yaml:"suggestion_buffer_percentage"`
	SuggestionMinimum          ResourceMinimum     `yaml:"suggestion_minimum"`
	RateWindow                 time.Duration       `yaml:"rate_window"`
	QueryStep                  string              `yaml:"query_step"`
}

type ResourceMemory struct {
	Enabled                    bool                `yaml:"enabled"`
	Range                      Duration            `yaml:"range"`
	OOMKilledCheck             bool                `yaml:"oomkilled_check"`
	OOMKilledLimitMultiplier   float64             `yaml:"oomkilled_limit_multiplier"`
	Percentiles                ResourcePercentiles `yaml:"percentiles"`
	SuggestionBufferPercentage Percentages         `yaml:"suggestion_buffer_percentage"`
	SuggestionMinimum          ResourceMinimum     `yaml:"suggestion_minimum"`
	RateWindow                 time.Duration       `yaml:"rate_window"`
}

type ResourcePercentiles struct {
	Request float64 `yaml:"request"`
	Limit   float64 `yaml:"limit"`
}

type Percentages struct {
	Request float64 `yaml:"request"`
	Limit   float64 `yaml:"limit"`
}

// ResourceMinimum holds the smallest suggestion the analysis may emit. Values
// are Kubernetes resource quantities, for example "10m" or "64Mi".
type ResourceMinimum struct {
	Request string `yaml:"request"`
	Limit   string `yaml:"limit"`
}

// Duration accepts an empty string as zero, which is used by resource ranges.
type Duration time.Duration

// UnmarshalYAML parses "", a duration string, or a whole number of seconds.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	value := strings.TrimSpace(node.Value)
	if value == "" {
		*d = 0
		return nil
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		*d = Duration(time.Duration(seconds) * time.Second)
		return nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("invalid duration %q (use a duration such as 6s or a whole number of seconds): %w", value, err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML renders zero as an empty string and other values as durations.
func (d Duration) MarshalYAML() (any, error) {
	if d == 0 {
		return "", nil
	}
	return time.Duration(d).String(), nil
}

// Kubernetes groups Kubernetes API connection settings. Kubernetes access is
// mandatory, so there is no enabled switch.
type Kubernetes struct {
	AccessMode     string   `yaml:"access_mode"`
	KubeconfigPath string   `yaml:"kubeconfig_path"`
	RequestTimeout Duration `yaml:"request_timeout"`
	PageSize       int      `yaml:"page_size"`
}

// Skin contains all theme colors used by the header and status components.
type Skin struct {
	Body   SkinBody   `yaml:"body"`
	Info   SkinInfo   `yaml:"info"`
	Frame  SkinFrame  `yaml:"frame"`
	Status SkinStatus `yaml:"status"`
}

type SkinBody struct {
	FGColor   string `yaml:"fgColor"`
	BGColor   string `yaml:"bgColor"`
	LogoColor string `yaml:"logoColor"`
}

type SkinInfo struct {
	FGColor      string `yaml:"fgColor"`
	SectionColor string `yaml:"sectionColor"`
}

type SkinFrame struct {
	Menu SkinMenu `yaml:"menu"`
}

type SkinMenu struct {
	FGColor     string `yaml:"fgColor"`
	KeyColor    string `yaml:"keyColor"`
	NumKeyColor string `yaml:"numKeyColor"`
}

type SkinStatus struct {
	ErrorColor string `yaml:"errorColor"`
	WarnColor  string `yaml:"warnColor"`
	InfoColor  string `yaml:"infoColor"`
}

// PrometheusContext holds the cluster-dependent settings that moved from
// config.yaml to state.yaml. It is used only to migrate legacy files once.
type PrometheusContext struct {
	URL              string
	SSLVerify        *bool
	TenantHeader     string
	BearerTokenEnv   string
	BasicUsernameEnv string
	BasicPasswordEnv string
	LabelFilters     map[string]string
}

// Snapshot is a pure merge of defaults and a user configuration.
type Snapshot struct {
	Config           Config
	EffectiveYAML    []byte
	Warnings         []string
	LegacyPrometheus *PrometheusContext
}

// Loaded is the result of loading the active configuration from disk.
type Loaded struct {
	Config           Config
	Path             string
	DefaultPath      string
	EffectiveYAML    []byte
	Warnings         []string
	LegacyPrometheus *PrometheusContext

	// Backups created while loading, if any.
	ConfigBackup  string
	DefaultBackup string
}

// Default returns the embedded default configuration.
func Default() Config {
	snapshot, err := Merge(nil)
	if err != nil {
		// The embedded defaults are part of the binary; this is a build bug.
		return Config{
			Keybindings: registry.Default().BindingDefaults(),
			Prometheus:  DefaultPrometheus(),
			Resources:   DefaultResources(),
			Kubernetes: Kubernetes{
				AccessMode:     "environment",
				RequestTimeout: Duration(6 * time.Second),
				PageSize:       100,
			},
		}
	}
	return snapshot.Config
}

// DefaultPrometheus returns the built-in global Prometheus defaults. Per-
// context connection settings start empty and come from state.yaml.
func DefaultPrometheus() Prometheus {
	return Prometheus{
		Enabled:           true,
		ConnectionTimeout: 10 * time.Second,
		RequestTimeout:    30 * time.Second,
		QueryRange:        24 * time.Hour,
		RateWindow:        5 * time.Minute,
		MaxConcurrent:     2,
	}
}

// DefaultResources returns the built-in resource-analysis defaults.
func DefaultResources() Resources {
	return Resources{
		CPU: ResourceCPU{
			Enabled:                    true,
			Percentiles:                ResourcePercentiles{Request: 90},
			SuggestionBufferPercentage: Percentages{Request: 10},
			SuggestionMinimum:          ResourceMinimum{Request: "10m", Limit: "10m"},
			RateWindow:                 5 * time.Minute,
			QueryStep:                  "auto",
		},
		Memory: ResourceMemory{
			Enabled:                    true,
			OOMKilledCheck:             true,
			OOMKilledLimitMultiplier:   1.25,
			Percentiles:                ResourcePercentiles{Request: 90, Limit: 99},
			SuggestionBufferPercentage: Percentages{Limit: 10},
			SuggestionMinimum:          ResourceMinimum{Request: "32Mi", Limit: "32Mi"},
			RateWindow:                 5 * time.Minute,
		},
	}
}

// Parse merges raw YAML over the embedded defaults and validates the result.
func Parse(raw []byte) (Config, error) {
	snapshot, err := Merge(raw)
	if err != nil {
		return Config{}, err
	}
	return snapshot.Config, nil
}

// Merge merges user YAML over the embedded defaults. Unknown user settings are
// preserved, normalized values are written back into the map, and the returned
// bytes are the comment-free effective configuration.
func Merge(user []byte) (Snapshot, error) {
	base, err := decodeMap([]byte(DefaultYAML))
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse embedded default config: %w", err)
	}

	merged := base
	if len(bytes.TrimSpace(user)) > 0 {
		override, err := decodeMap(user)
		if err != nil {
			return Snapshot{}, fmt.Errorf("parse config: %w", err)
		}
		merged = mergeMaps(base, override)
	}
	delete(merged, "columns")
	warnings, legacyPrometheus := removeLegacyKeys(merged)
	if keybindings, ok := merged["keybindings"].(map[string]any); ok {
		for id := range keybindings {
			if strings.HasPrefix(id, "column_toggle_") || id == "prometheus_connect" {
				delete(keybindings, id)
			}
		}
	}

	raw, err := marshalYAML(merged)
	if err != nil {
		return Snapshot{}, fmt.Errorf("merge config: %w", err)
	}

	cfg := Config{}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Snapshot{}, fmt.Errorf("parse effective config: %w", err)
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return Snapshot{}, err
	}

	applyConfig(merged, cfg)
	effective, err := marshalYAML(merged)
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode effective config: %w", err)
	}

	return Snapshot{Config: cfg, EffectiveYAML: effective, Warnings: warnings, LegacyPrometheus: legacyPrometheus}, nil
}

// removeLegacyKeys drops settings that are no longer part of the schema.
// Legacy input is ignored instead of rejected so existing user files keep
// working. The returned warnings are intended to be shown once by the caller.
func removeLegacyKeys(values map[string]any) ([]string, *PrometheusContext) {
	warnings := make([]string, 0, 2)
	if kubernetes, ok := values["kubernetes"].(map[string]any); ok {
		if _, exists := kubernetes["enabled"]; exists {
			delete(kubernetes, "enabled")
			warnings = append(warnings, "kubernetes.enabled is no longer supported and was ignored; Kubernetes access is always required")
		}
	}

	legacy := (*PrometheusContext)(nil)
	if prometheus, ok := values["prometheus"].(map[string]any); ok {
		legacy = removeMovedPrometheusKeys(prometheus)
		if legacy != nil {
			warnings = append(warnings, "prometheus.url, prometheus.ssl_verify, prometheus.tenant_header, prometheus.bearer_token_env, prometheus.basic_username_env, prometheus.basic_password_env, and prometheus.label_filters now live per context in state.yaml and were ignored in config.yaml")
		}
	}
	return warnings, legacy
}

func removeMovedPrometheusKeys(prometheus map[string]any) *PrometheusContext {
	moved := []string{
		"url", "ssl_verify", "tenant_header", "bearer_token_env",
		"basic_username_env", "basic_password_env", "label_filters",
	}
	present := false
	for _, key := range moved {
		if _, ok := prometheus[key]; ok {
			present = true
			break
		}
	}
	if !present {
		return nil
	}

	raw, err := yaml.Marshal(prometheus)
	if err != nil {
		return nil
	}
	var parsed struct {
		URL              string            `yaml:"url"`
		SSLVerify        *bool             `yaml:"ssl_verify"`
		TenantHeader     string            `yaml:"tenant_header"`
		BearerTokenEnv   string            `yaml:"bearer_token_env"`
		BasicUsernameEnv string            `yaml:"basic_username_env"`
		BasicPasswordEnv string            `yaml:"basic_password_env"`
		LabelFilters     map[string]string `yaml:"label_filters"`
	}
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	for _, key := range moved {
		delete(prometheus, key)
	}
	return &PrometheusContext{
		URL:              strings.TrimSpace(parsed.URL),
		SSLVerify:        parsed.SSLVerify,
		TenantHeader:     strings.TrimSpace(parsed.TenantHeader),
		BearerTokenEnv:   strings.TrimSpace(parsed.BearerTokenEnv),
		BasicUsernameEnv: strings.TrimSpace(parsed.BasicUsernameEnv),
		BasicPasswordEnv: strings.TrimSpace(parsed.BasicPasswordEnv),
		LabelFilters:     parsed.LabelFilters,
	}
}

// marshalYAML encodes YAML with the two-space indentation used by the
// repository's embedded defaults and persisted state.
func marshalYAML(value any) ([]byte, error) {
	var node yaml.Node
	if err := node.Encode(value); err != nil {
		return nil, err
	}
	keybindingsLast(&node)

	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&node); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// keybindingsLast moves the keybindings section to the end of the top-level
// mapping so generated effective configuration keeps the same shape as the
// documentation-rich defaults.
func keybindingsLast(node *yaml.Node) {
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) != 1 {
			return
		}
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return
	}
	content := node.Content
	for index := 0; index+1 < len(content); index += 2 {
		if content[index].Value != "keybindings" {
			continue
		}
		key, value := content[index], content[index+1]
		reordered := make([]*yaml.Node, 0, len(content))
		reordered = append(reordered, content[:index]...)
		reordered = append(reordered, content[index+2:]...)
		reordered = append(reordered, key, value)
		node.Content = reordered
		return
	}
}

// Normalize fills empty values with their defaults and lower-cases enum-like
// settings.
func (c *Config) Normalize() {
	c.Kubernetes.AccessMode = strings.ToLower(strings.TrimSpace(c.Kubernetes.AccessMode))
	if c.Kubernetes.AccessMode == "" {
		c.Kubernetes.AccessMode = "environment"
	}
	c.Kubernetes.KubeconfigPath = strings.TrimSpace(c.Kubernetes.KubeconfigPath)
	if c.Kubernetes.RequestTimeout <= 0 {
		c.Kubernetes.RequestTimeout = Duration(6 * time.Second)
	}
	if c.Kubernetes.PageSize <= 0 {
		c.Kubernetes.PageSize = 100
	}

	promDefaults := DefaultPrometheus()
	c.Prometheus.URL = strings.TrimSpace(c.Prometheus.URL)
	c.Prometheus.TenantHeader = strings.TrimSpace(c.Prometheus.TenantHeader)
	c.Prometheus.BearerTokenEnv = strings.TrimSpace(c.Prometheus.BearerTokenEnv)
	c.Prometheus.BasicUsernameEnv = strings.TrimSpace(c.Prometheus.BasicUsernameEnv)
	c.Prometheus.BasicPasswordEnv = strings.TrimSpace(c.Prometheus.BasicPasswordEnv)
	if c.Prometheus.ConnectionTimeout <= 0 {
		c.Prometheus.ConnectionTimeout = promDefaults.ConnectionTimeout
	}
	if c.Prometheus.RequestTimeout <= 0 {
		c.Prometheus.RequestTimeout = promDefaults.RequestTimeout
	}
	if c.Prometheus.QueryRange <= 0 {
		c.Prometheus.QueryRange = promDefaults.QueryRange
	}
	if c.Prometheus.RateWindow <= 0 {
		c.Prometheus.RateWindow = promDefaults.RateWindow
	}
	if c.Prometheus.MaxConcurrent <= 0 {
		c.Prometheus.MaxConcurrent = promDefaults.MaxConcurrent
	}
	if c.Prometheus.LabelFilters == nil {
		c.Prometheus.LabelFilters = make(map[string]string)
	}
	filters := make(map[string]string, len(c.Prometheus.LabelFilters))
	for key, value := range c.Prometheus.LabelFilters {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		filters[key] = value
	}
	c.Prometheus.LabelFilters = filters

	resourceDefaults := DefaultResources()
	if c.Resources.CPU.RateWindow <= 0 {
		c.Resources.CPU.RateWindow = c.Prometheus.RateWindow
	}
	if c.Resources.Memory.RateWindow <= 0 {
		c.Resources.Memory.RateWindow = c.Prometheus.RateWindow
	}
	c.Resources.CPU.QueryStep = strings.ToLower(strings.TrimSpace(c.Resources.CPU.QueryStep))
	if c.Resources.CPU.QueryStep == "" {
		c.Resources.CPU.QueryStep = resourceDefaults.CPU.QueryStep
	}
	normalizeResourceMinimum(&c.Resources.CPU.SuggestionMinimum)
	normalizeResourceMinimum(&c.Resources.Memory.SuggestionMinimum)

	if c.Keybindings == nil {
		c.Keybindings = make(map[string]string)
	}
	for id, value := range c.Keybindings {
		c.Keybindings[id] = registry.NormalizeKey(value)
	}
	for id, value := range registry.Default().BindingDefaults() {
		if strings.TrimSpace(c.Keybindings[id]) == "" {
			c.Keybindings[id] = value
		}
	}
	migrateKeybindings(c.Keybindings)
	c.View.SortColumn = strings.ToUpper(strings.TrimSpace(c.View.SortColumn))
}

func normalizeResourceMinimum(value *ResourceMinimum) {
	value.Request = strings.TrimSpace(value.Request)
	if value.Request == "" {
		value.Request = "0"
	}
	value.Limit = strings.TrimSpace(value.Limit)
	if value.Limit == "" {
		value.Limit = "0"
	}
}

// PrometheusOverride carries mutable per-cluster Prometheus settings from
// state.yaml. Zero values mean "no override"; use pointers where false is a
// meaningful value.
type PrometheusOverride struct {
	URL              string
	SSLVerify        *bool
	TenantHeader     string
	BearerTokenEnv   string
	BasicUsernameEnv string
	BasicPasswordEnv string
	LabelFilters     map[string]string
}

// ApplyPrometheusOverride applies per-cluster state over configured defaults.
func (c *Config) ApplyPrometheusOverride(override PrometheusOverride) {
	if strings.TrimSpace(override.URL) != "" {
		c.Prometheus.URL = strings.TrimSpace(override.URL)
	}
	if override.SSLVerify != nil {
		c.Prometheus.SSLVerify = *override.SSLVerify
	}
	if strings.TrimSpace(override.TenantHeader) != "" {
		c.Prometheus.TenantHeader = strings.TrimSpace(override.TenantHeader)
	}
	if strings.TrimSpace(override.BearerTokenEnv) != "" {
		c.Prometheus.BearerTokenEnv = strings.TrimSpace(override.BearerTokenEnv)
	}
	if strings.TrimSpace(override.BasicUsernameEnv) != "" {
		c.Prometheus.BasicUsernameEnv = strings.TrimSpace(override.BasicUsernameEnv)
	}
	if strings.TrimSpace(override.BasicPasswordEnv) != "" {
		c.Prometheus.BasicPasswordEnv = strings.TrimSpace(override.BasicPasswordEnv)
	}
	if override.LabelFilters != nil {
		c.Prometheus.LabelFilters = make(map[string]string, len(override.LabelFilters))
		for key, value := range override.LabelFilters {
			c.Prometheus.LabelFilters[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
}

func migrateKeybindings(bindings map[string]string) {
	legacy := map[string]map[string]string{
		"options": {
			"ctrl+o": "ctrl+p",
		},
		"toggle_replicas": {
			"ctrl+p": "ctrl+t",
		},
		"select_previous_column": {
			"shift+alt+left": "H",
			"alt+h":          "H",
		},
		"select_next_column": {
			"shift+alt+right": "L",
			"alt+l":           "L",
		},
		"sort_ascending": {
			"shift+alt+a": "<",
			"alt+j":       "<",
		},
		"sort_descending": {
			"shift+alt+d": ">",
			"alt+k":       ">",
		},
	}
	for id, replacements := range legacy {
		if replacement, ok := replacements[bindings[id]]; ok {
			bindings[id] = replacement
		}
	}
	for id := range bindings {
		if strings.HasPrefix(id, "column_toggle_") || id == "prometheus_connect" {
			delete(bindings, id)
		}
	}
}

// Validate checks settings that cannot be safely normalized.
func (c Config) Validate() error {
	switch c.Kubernetes.AccessMode {
	case "environment":
	case "file":
		if c.Kubernetes.KubeconfigPath == "" {
			return errors.New("kubernetes.kubeconfig_path is required when access_mode is \"file\"")
		}
	default:
		return fmt.Errorf("kubernetes.access_mode %q is invalid (use environment or file)", c.Kubernetes.AccessMode)
	}

	if err := validatePrometheus(c.Prometheus); err != nil {
		return err
	}
	if err := validateResourceGroup("resources.cpu", c.Resources.CPU.Enabled, c.Resources.CPU.Range, c.Resources.CPU.Percentiles, c.Resources.CPU.SuggestionBufferPercentage, c.Resources.CPU.SuggestionMinimum); err != nil {
		return err
	}
	if err := validateResourceGroup("resources.memory", c.Resources.Memory.Enabled, c.Resources.Memory.Range, c.Resources.Memory.Percentiles, c.Resources.Memory.SuggestionBufferPercentage, c.Resources.Memory.SuggestionMinimum); err != nil {
		return err
	}
	if c.Resources.Memory.OOMKilledLimitMultiplier < 0 {
		return errors.New("resources.memory.oomkilled_limit_multiplier must not be negative")
	}
	if c.Resources.CPU.RateWindow <= 0 {
		return errors.New("resources.cpu.rate_window must be a positive duration")
	}
	if c.Resources.Memory.RateWindow <= 0 {
		return errors.New("resources.memory.rate_window must be a positive duration")
	}
	if c.Resources.CPU.QueryStep != "auto" {
		step, err := time.ParseDuration(c.Resources.CPU.QueryStep)
		if err != nil || step <= 0 {
			return fmt.Errorf("resources.cpu.query_step %q must be \"auto\" or a positive duration", c.Resources.CPU.QueryStep)
		}
	}

	if err := registry.Default().WithBindings(c.Keybindings).ValidateBindings(); err != nil {
		return err
	}

	return nil
}

// ValidatePrometheusURL accepts only http and https URLs without userinfo.
func ValidatePrometheusURL(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return errors.New("prometheus URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("prometheus URL must start with http:// or https://")
	}
	if parsed.Host == "" {
		return errors.New("prometheus URL has no host")
	}
	if parsed.User != nil {
		return errors.New("prometheus URL must not contain credentials; use the auth environment-variable names instead")
	}
	return nil
}

// ValidateEnvironmentName checks a shell environment-variable name.
func ValidateEnvironmentName(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for index, r := range value {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || index > 0 && r >= '0' && r <= '9' {
			continue
		}
		return fmt.Errorf("%q is not a valid environment-variable name", value)
	}
	return nil
}

// ValidateLabelFilters checks Prometheus equality-label names and values.
func ValidateLabelFilters(filters map[string]string) error {
	for key, value := range filters {
		key = strings.TrimSpace(key)
		if key == "" {
			return errors.New("label filter name cannot be empty")
		}
		for index, r := range key {
			if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || index > 0 && r >= '0' && r <= '9' {
				continue
			}
			return fmt.Errorf("%q is not a valid Prometheus label name", key)
		}
		if strings.ContainsAny(value, "\n\r") {
			return fmt.Errorf("label filter %q value cannot contain newlines", key)
		}
	}
	return nil
}

// ParseLabelFilters parses comma-separated key=value equality filters.
func ParseLabelFilters(raw string) (map[string]string, error) {
	filters := make(map[string]string)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		index := strings.Index(part, "=")
		if index < 0 {
			return nil, fmt.Errorf("label filter %q must use key=value", part)
		}
		key := strings.TrimSpace(part[:index])
		value := strings.TrimSpace(part[index+1:])
		if key == "" {
			return nil, errors.New("label filter name cannot be empty")
		}
		filters[key] = value
	}
	if err := ValidateLabelFilters(filters); err != nil {
		return nil, err
	}
	return filters, nil
}

// FormatLabelFilters renders equality filters as comma-separated key=value.
func FormatLabelFilters(filters map[string]string) string {
	keys := make([]string, 0, len(filters))
	for key := range filters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+filters[key])
	}
	return strings.Join(parts, ", ")
}

func validatePrometheus(value Prometheus) error {
	if err := ValidatePrometheusURL(value.URL); err != nil {
		return err
	}
	if err := ValidateEnvironmentName(value.BearerTokenEnv); err != nil {
		return fmt.Errorf("prometheus.bearer_token_env: %w", err)
	}
	if err := ValidateEnvironmentName(value.BasicUsernameEnv); err != nil {
		return fmt.Errorf("prometheus.basic_username_env: %w", err)
	}
	if err := ValidateEnvironmentName(value.BasicPasswordEnv); err != nil {
		return fmt.Errorf("prometheus.basic_password_env: %w", err)
	}
	if err := ValidateLabelFilters(value.LabelFilters); err != nil {
		return fmt.Errorf("prometheus.label_filters: %w", err)
	}
	if value.ConnectionTimeout <= 0 {
		return errors.New("prometheus.connection_timeout must be a positive duration")
	}
	if value.RequestTimeout <= 0 {
		return errors.New("prometheus.request_timeout must be a positive duration")
	}
	if value.QueryRange <= 0 {
		return errors.New("prometheus.query_range must be a positive duration")
	}
	if value.RateWindow <= 0 {
		return errors.New("prometheus.rate_window must be a positive duration")
	}
	if value.MaxConcurrent <= 0 {
		return errors.New("prometheus.max_concurrent_queries must be positive")
	}
	return nil
}

func validateResourceGroup(path string, enabled bool, rangeValue Duration, percentiles ResourcePercentiles, buffers Percentages, minimums ResourceMinimum) error {
	if rangeValue < 0 {
		return fmt.Errorf("%s.range must be empty or a positive duration", path)
	}
	for name, value := range map[string]float64{
		"request": percentiles.Request,
		"limit":   percentiles.Limit,
	} {
		if value < 0 || value > 100 {
			return fmt.Errorf("%s.percentiles.%s must be between 0 and 100", path, name)
		}
	}
	for name, value := range map[string]float64{
		"request": buffers.Request,
		"limit":   buffers.Limit,
	} {
		if value < 0 {
			return fmt.Errorf("%s.suggestion_buffer_percentage.%s must not be negative", path, name)
		}
	}
	for name, value := range map[string]string{
		"request": minimums.Request,
		"limit":   minimums.Limit,
	} {
		quantity, err := resource.ParseQuantity(strings.TrimSpace(value))
		if err != nil || quantity.Sign() < 0 {
			return fmt.Errorf("%s.suggestion_minimum.%s must be a non-negative Kubernetes resource quantity", path, name)
		}
	}
	return nil
}

// Dir returns the XDG configuration directory for Kubernova.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve XDG config directory: %w", err)
	}
	return filepath.Join(base, "kubernova"), nil
}

// DefaultPath returns the generated config-default.yaml path.
func DefaultPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config-default.yaml"), nil
}

// EffectivePath returns the active config.yaml path.
func EffectivePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// BackupDir returns the directory used for configuration backups.
func BackupDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "backup"), nil
}

// Paths returns the generated default path and the effective config path.
func Paths() (defaultPath, effectivePath string, err error) {
	defaultPath, err = DefaultPath()
	if err != nil {
		return "", "", err
	}
	effectivePath, err = EffectivePath()
	if err != nil {
		return "", "", err
	}
	return defaultPath, effectivePath, nil
}

// Load ensures the generated default file and the effective config file are
// current, then returns the active configuration.
func Load() (Loaded, error) {
	defaultPath, effectivePath, err := Paths()
	if err != nil {
		return Loaded{}, err
	}

	loaded := Loaded{Path: effectivePath, DefaultPath: defaultPath}

	backup, err := EnsureDefault()
	loaded.DefaultBackup = backup
	if err != nil {
		return loaded, err
	}

	raw, err := os.ReadFile(effectivePath)
	if errors.Is(err, fs.ErrNotExist) {
		snapshot, mergeErr := Merge(nil)
		if mergeErr != nil {
			return loaded, mergeErr
		}
		if writeErr := writeFile(effectivePath, snapshot.EffectiveYAML); writeErr != nil {
			return loaded, writeErr
		}
		loaded.Config = snapshot.Config
		loaded.EffectiveYAML = snapshot.EffectiveYAML
		loaded.Warnings = snapshot.Warnings
		loaded.LegacyPrometheus = snapshot.LegacyPrometheus
		return loaded, nil
	}
	if err != nil {
		return loaded, fmt.Errorf("read config %q: %w", effectivePath, err)
	}

	snapshot, err := Merge(raw)
	if err != nil {
		return loaded, fmt.Errorf("parse config %q: %w", effectivePath, err)
	}

	if !bytes.Equal(bytes.TrimSpace(raw), bytes.TrimSpace(snapshot.EffectiveYAML)) {
		backup, err := Backup(effectivePath)
		if err != nil {
			return loaded, err
		}
		loaded.ConfigBackup = backup
		if err := writeFile(effectivePath, snapshot.EffectiveYAML); err != nil {
			return loaded, err
		}
	}

	loaded.Config = snapshot.Config
	loaded.EffectiveYAML = snapshot.EffectiveYAML
	loaded.Warnings = snapshot.Warnings
	loaded.LegacyPrometheus = snapshot.LegacyPrometheus
	return loaded, nil
}

// UnknownEqual reports whether two effective configurations have identical
// values outside the sections managed by this application. Unknown sections
// require a restart because the running app has no handler for them.
func UnknownEqual(left, right []byte) (bool, error) {
	leftValues, err := decodeMap(left)
	if err != nil {
		return false, err
	}
	rightValues, err := decodeMap(right)
	if err != nil {
		return false, err
	}
	removeKnownValues(leftValues)
	removeKnownValues(rightValues)
	return reflect.DeepEqual(pruneEmpty(leftValues), pruneEmpty(rightValues)), nil
}

func removeKnownValues(values map[string]any) {
	for _, path := range [][]string{
		{"keybindings"},
		{"view"},
		{"prometheus"},
		{"prometheus", "url"},
		{"prometheus", "ssl_verify"},
		{"prometheus", "tenant_header"},
		{"prometheus", "bearer_token_env"},
		{"prometheus", "basic_username_env"},
		{"prometheus", "basic_password_env"},
		{"prometheus", "label_filters"},
		{"resources"},
		{"kubernetes", "enabled"},
		{"kubernetes", "access_mode"},
		{"kubernetes", "kubeconfig_path"},
		{"kubernetes", "request_timeout"},
		{"kubernetes", "page_size"},
		{"skin", "body", "fgColor"},
		{"skin", "body", "bgColor"},
		{"skin", "body", "logoColor"},
		{"skin", "info", "fgColor"},
		{"skin", "info", "sectionColor"},
		{"skin", "frame", "menu", "fgColor"},
		{"skin", "frame", "menu", "keyColor"},
		{"skin", "frame", "menu", "numKeyColor"},
		{"skin", "status", "errorColor"},
		{"skin", "status", "warnColor"},
		{"skin", "status", "infoColor"},
	} {
		deletePath(values, path...)
	}
}

func deletePath(values map[string]any, path ...string) {
	if len(path) == 0 {
		return
	}
	if len(path) == 1 {
		delete(values, path[0])
		return
	}
	child, ok := values[path[0]].(map[string]any)
	if !ok {
		return
	}
	deletePath(child, path[1:]...)
}

func pruneEmpty(values map[string]any) map[string]any {
	pruned := make(map[string]any, len(values))
	for key, value := range values {
		if child, ok := value.(map[string]any); ok {
			child = pruneEmpty(child)
			if len(child) == 0 {
				continue
			}
			pruned[key] = child
			continue
		}
		pruned[key] = value
	}
	return pruned
}

// EnsureDefault regenerates config-default.yaml when it is missing or differs
// from the embedded defaults. It returns the backup path when an outdated file
// was replaced.
func EnsureDefault() (string, error) {
	path, err := DefaultPath()
	if err != nil {
		return "", err
	}
	want := []byte(DefaultYAML)

	existing, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := writeFile(path, want); err != nil {
			return "", err
		}
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read default config %q: %w", path, err)
	}
	if bytes.Equal(existing, want) {
		return "", nil
	}

	backup, err := Backup(path)
	if err != nil {
		return "", err
	}
	if err := writeFile(path, want); err != nil {
		return backup, err
	}
	return backup, nil
}

// Backup copies an existing configuration file into the backup directory and
// keeps at most three backups in total. A missing source file is not an error.
func Backup(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read config for backup %q: %w", path, err)
	}

	dir, err := BackupDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}

	// Free a slot before creating a fourth backup so the oldest file is
	// removed first.
	pruneBackups(2)

	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)
	name := fmt.Sprintf("%s-%s%s", base, time.Now().UTC().Format("20060102T150405.000000000"), ext)
	destination := filepath.Join(dir, name)
	if err := os.WriteFile(destination, raw, 0o600); err != nil {
		return "", fmt.Errorf("write backup %q: %w", destination, err)
	}
	pruneBackups(3)
	return destination, nil
}

func pruneBackups(max int) {
	dir, err := BackupDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	type backup struct {
		name    string
		modTime time.Time
	}
	backups := make([]backup, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		backups = append(backups, backup{name: entry.Name(), modTime: info.ModTime()})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].modTime.Equal(backups[j].modTime) {
			return backups[i].name < backups[j].name
		}
		return backups[i].modTime.Before(backups[j].modTime)
	})
	for len(backups) > max {
		_ = os.Remove(filepath.Join(dir, backups[0].name))
		backups = backups[1:]
	}
}

func writeFile(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write config %q: %w", path, err)
	}
	return nil
}

func decodeMap(raw []byte) (map[string]any, error) {
	var values map[string]any
	if err := yaml.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	if values == nil {
		values = make(map[string]any)
	}
	return values, nil
}

func mergeMaps(base, override map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(override))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range override {
		baseChild, baseOK := merged[key].(map[string]any)
		overrideChild, overrideOK := value.(map[string]any)
		if baseOK && overrideOK {
			merged[key] = mergeMaps(baseChild, overrideChild)
			continue
		}
		merged[key] = value
	}
	return merged
}

func applyConfig(values map[string]any, cfg Config) {
	kubernetes := childMap(values, "kubernetes")
	kubernetes["access_mode"] = cfg.Kubernetes.AccessMode
	kubernetes["kubeconfig_path"] = cfg.Kubernetes.KubeconfigPath
	kubernetes["request_timeout"] = cfg.Kubernetes.RequestTimeout
	kubernetes["page_size"] = cfg.Kubernetes.PageSize

	keybindings := childMap(values, "keybindings")
	for id, value := range cfg.Keybindings {
		keybindings[id] = value
	}

	view := childMap(values, "view")
	view["sort_column"] = cfg.View.SortColumn
	view["sort_descending"] = cfg.View.SortDescending
	view["fullscreen"] = cfg.View.Fullscreen

	prometheus := childMap(values, "prometheus")
	prometheus["enabled"] = cfg.Prometheus.Enabled
	prometheus["connection_timeout"] = cfg.Prometheus.ConnectionTimeout
	prometheus["request_timeout"] = cfg.Prometheus.RequestTimeout
	prometheus["query_range"] = cfg.Prometheus.QueryRange
	prometheus["rate_window"] = cfg.Prometheus.RateWindow
	prometheus["max_concurrent_queries"] = cfg.Prometheus.MaxConcurrent

	resources := childMap(values, "resources")
	cpu := childMap(resources, "cpu")
	cpu["enabled"] = cfg.Resources.CPU.Enabled
	cpu["range"] = cfg.Resources.CPU.Range
	cpuPercentiles := childMap(cpu, "percentiles")
	cpuPercentiles["request"] = cfg.Resources.CPU.Percentiles.Request
	cpuPercentiles["limit"] = cfg.Resources.CPU.Percentiles.Limit
	cpuBuffers := childMap(cpu, "suggestion_buffer_percentage")
	cpuBuffers["request"] = cfg.Resources.CPU.SuggestionBufferPercentage.Request
	cpuBuffers["limit"] = cfg.Resources.CPU.SuggestionBufferPercentage.Limit
	cpuMinimum := childMap(cpu, "suggestion_minimum")
	cpuMinimum["request"] = cfg.Resources.CPU.SuggestionMinimum.Request
	cpuMinimum["limit"] = cfg.Resources.CPU.SuggestionMinimum.Limit
	cpu["rate_window"] = cfg.Resources.CPU.RateWindow
	cpu["query_step"] = cfg.Resources.CPU.QueryStep

	memory := childMap(resources, "memory")
	memory["enabled"] = cfg.Resources.Memory.Enabled
	memory["range"] = cfg.Resources.Memory.Range
	memory["oomkilled_check"] = cfg.Resources.Memory.OOMKilledCheck
	memory["oomkilled_limit_multiplier"] = cfg.Resources.Memory.OOMKilledLimitMultiplier
	memoryPercentiles := childMap(memory, "percentiles")
	memoryPercentiles["request"] = cfg.Resources.Memory.Percentiles.Request
	memoryPercentiles["limit"] = cfg.Resources.Memory.Percentiles.Limit
	memoryBuffers := childMap(memory, "suggestion_buffer_percentage")
	memoryBuffers["request"] = cfg.Resources.Memory.SuggestionBufferPercentage.Request
	memoryBuffers["limit"] = cfg.Resources.Memory.SuggestionBufferPercentage.Limit
	memoryMinimum := childMap(memory, "suggestion_minimum")
	memoryMinimum["request"] = cfg.Resources.Memory.SuggestionMinimum.Request
	memoryMinimum["limit"] = cfg.Resources.Memory.SuggestionMinimum.Limit
	memory["rate_window"] = cfg.Resources.Memory.RateWindow
}

func childMap(parent map[string]any, key string) map[string]any {
	if child, ok := parent[key].(map[string]any); ok {
		return child
	}
	child := make(map[string]any)
	parent[key] = child
	return child
}
