package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func useTempConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func writeConfigFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func lastTopLevelKey(raw string) string {
	last := ""
	for _, line := range strings.Split(raw, "\n") {
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "#") {
			continue
		}
		last = line
	}
	return last
}

func hasYAMLComment(raw []byte) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			return true
		}
	}
	return false
}

func backupFiles(t *testing.T) []string {
	t.Helper()
	dir, err := BackupDir()
	if err != nil {
		t.Fatalf("BackupDir() error = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read backup dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names
}

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Kubernetes.AccessMode != "environment" {
		t.Errorf("access mode = %q, want environment", cfg.Kubernetes.AccessMode)
	}
	if time.Duration(cfg.Kubernetes.RequestTimeout) != 6*time.Second {
		t.Errorf("request timeout = %v, want 6s", cfg.Kubernetes.RequestTimeout)
	}
	if cfg.Kubernetes.PageSize != 100 {
		t.Errorf("page size = %d, want 100", cfg.Kubernetes.PageSize)
	}
	if cfg.Skin.Body.BGColor != "#000000" || cfg.Skin.Frame.Menu.NumKeyColor == "" {
		t.Errorf("skin defaults are incomplete: %+v", cfg.Skin)
	}
	if !strings.Contains(DefaultYAML, "access_mode") || !strings.Contains(DefaultYAML, "page_size") {
		t.Error("embedded defaults are missing documented values")
	}
}

func TestLoadCreatesDefaultAndEffectiveFiles(t *testing.T) {
	dir := useTempConfigHome(t)

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if want := filepath.Join(dir, "kubernova", "config.yaml"); loaded.Path != want {
		t.Errorf("effective path = %q, want %q", loaded.Path, want)
	}
	if want := filepath.Join(dir, "kubernova", "config-default.yaml"); loaded.DefaultPath != want {
		t.Errorf("default path = %q, want %q", loaded.DefaultPath, want)
	}
	if loaded.Config.Kubernetes.PageSize != 100 {
		t.Errorf("page size = %d, want 100", loaded.Config.Kubernetes.PageSize)
	}

	defaultRaw, err := os.ReadFile(loaded.DefaultPath)
	if err != nil {
		t.Fatalf("read default file: %v", err)
	}
	if string(defaultRaw) != DefaultYAML {
		t.Error("generated config-default.yaml does not match embedded defaults")
	}
	if !bytes.Contains(defaultRaw, []byte("#")) {
		t.Error("generated config-default.yaml has no explanatory comments")
	}

	effectiveRaw, err := os.ReadFile(loaded.Path)
	if err != nil {
		t.Fatalf("read effective file: %v", err)
	}
	if hasYAMLComment(effectiveRaw) {
		t.Errorf("effective config.yaml contains comments:\n%s", effectiveRaw)
	}
	if !bytes.Contains(effectiveRaw, []byte("page_size: 100")) {
		t.Errorf("effective config.yaml is incomplete:\n%s", effectiveRaw)
	}
}

func TestLoadIsStableAcrossRuns(t *testing.T) {
	useTempConfigHome(t)

	first, err := Load()
	if err != nil {
		t.Fatalf("first Load() error = %v", err)
	}
	second, err := Load()
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	if second.ConfigBackup != "" || second.DefaultBackup != "" {
		t.Errorf("second Load() rewrote a stable config (backups: %q, %q)", second.ConfigBackup, second.DefaultBackup)
	}
	if !bytes.Equal(first.EffectiveYAML, second.EffectiveYAML) {
		t.Error("effective config changed between identical loads")
	}
	if len(backupFiles(t)) != 0 {
		t.Error("stable loads created backups")
	}
}

func TestLoadMergesPartialConfigAndPreservesUnknownValues(t *testing.T) {
	useTempConfigHome(t)
	path, err := EffectivePath()
	if err != nil {
		t.Fatalf("EffectivePath() error = %v", err)
	}
	writeConfigFile(t, path, `kubernetes:
  page_size: 25
future:
  feature:
    enabled: true
    label: keep-me
`)

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Config.Kubernetes.PageSize != 25 {
		t.Errorf("page size = %d, want 25", loaded.Config.Kubernetes.PageSize)
	}
	if time.Duration(loaded.Config.Kubernetes.RequestTimeout) != 6*time.Second {
		t.Errorf("request timeout = %v, want default 6s", loaded.Config.Kubernetes.RequestTimeout)
	}
	if loaded.ConfigBackup == "" {
		t.Error("Load() did not create a backup before rewriting config.yaml")
	}
	if !bytes.Contains(loaded.EffectiveYAML, []byte("keep-me")) {
		t.Errorf("effective config dropped unknown user values:\n%s", loaded.EffectiveYAML)
	}
}

func TestLoadCanonicalizesCommentsAndKeepsBackup(t *testing.T) {
	useTempConfigHome(t)
	path, err := EffectivePath()
	if err != nil {
		t.Fatalf("EffectivePath() error = %v", err)
	}
	writeConfigFile(t, path, "# comment\nkubernetes:\n  page_size: 12 # inline\n")

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.ConfigBackup == "" {
		t.Fatal("Load() did not create a backup")
	}
	if _, err := os.Stat(loaded.ConfigBackup); err != nil {
		t.Errorf("backup file is missing: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read effective config: %v", err)
	}
	if hasYAMLComment(raw) {
		t.Errorf("config.yaml still contains comments:\n%s", raw)
	}
}

func TestEnsureDefaultRegeneratesOutdatedFile(t *testing.T) {
	useTempConfigHome(t)
	defaultPath, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() error = %v", err)
	}
	writeConfigFile(t, defaultPath, "stale: true\n")

	backup, err := EnsureDefault()
	if err != nil {
		t.Fatalf("EnsureDefault() error = %v", err)
	}
	if backup == "" {
		t.Error("EnsureDefault() did not return a backup path")
	}
	raw, err := os.ReadFile(defaultPath)
	if err != nil {
		t.Fatalf("read default file: %v", err)
	}
	if string(raw) != DefaultYAML {
		t.Error("outdated config-default.yaml was not regenerated")
	}
}

func TestBackupKeepsOnlyThreeCopies(t *testing.T) {
	useTempConfigHome(t)
	path, err := EffectivePath()
	if err != nil {
		t.Fatalf("EffectivePath() error = %v", err)
	}
	writeConfigFile(t, path, "kubernetes:\n  page_size: 1\n")

	first, err := Backup(path)
	if err != nil {
		t.Fatalf("first Backup() error = %v", err)
	}
	for index := 0; index < 3; index++ {
		if _, err := Backup(path); err != nil {
			t.Fatalf("Backup() error = %v", err)
		}
	}

	names := backupFiles(t)
	if len(names) != 3 {
		t.Fatalf("backup count = %d, want 3", len(names))
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("oldest backup %q was not removed", first)
	}
}

func TestLoadDoesNotOverwriteMalformedConfig(t *testing.T) {
	useTempConfigHome(t)
	path, err := EffectivePath()
	if err != nil {
		t.Fatalf("EffectivePath() error = %v", err)
	}
	malformed := "application: [\n"
	writeConfigFile(t, path, malformed)

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want a parse error")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(raw) != malformed {
		t.Error("Load() overwrote a malformed user configuration")
	}
}

func TestUnknownEqual(t *testing.T) {
	left := []byte("kubernetes:\n  page_size: 1\nfuture:\n  value: one\n")
	right := []byte("kubernetes:\n  page_size: 5\nfuture:\n  value: one\n")
	equal, err := UnknownEqual(left, right)
	if err != nil {
		t.Fatalf("UnknownEqual() error = %v", err)
	}
	if !equal {
		t.Error("UnknownEqual() = false, want true for known-section-only changes")
	}

	changed := []byte("future:\n  value: two\n")
	equal, err = UnknownEqual(left, changed)
	if err != nil {
		t.Fatalf("UnknownEqual() error = %v", err)
	}
	if equal {
		t.Error("UnknownEqual() = true, want false for an unknown-section change")
	}

	changed = []byte("kubernetes:\n  future_kubernetes_key: true\n")
	equal, err = UnknownEqual(left, changed)
	if err != nil {
		t.Fatalf("UnknownEqual() error = %v", err)
	}
	if equal {
		t.Error("UnknownEqual() = true, want false for an unknown nested value")
	}
}

func TestParseRejectsUnknownEnums(t *testing.T) {
	if _, err := Parse([]byte("kubernetes:\n  access_mode: magic\n")); err == nil {
		t.Fatal("Parse() error = nil, want invalid access_mode")
	}
}

func TestLegacyEnabledIsIgnoredWithWarning(t *testing.T) {
	snapshot, err := Merge([]byte("kubernetes:\n  enabled: false\n  access_mode: environment\n"))
	if err != nil {
		t.Fatalf("Merge() error = %v, want legacy enabled to be ignored", err)
	}
	if len(snapshot.Warnings) != 1 || !strings.Contains(snapshot.Warnings[0], "kubernetes.enabled") {
		t.Errorf("warnings = %v, want a legacy enabled warning", snapshot.Warnings)
	}
	values, err := decodeMap(snapshot.EffectiveYAML)
	if err != nil {
		t.Fatalf("decode effective config: %v", err)
	}
	kubernetes, _ := values["kubernetes"].(map[string]any)
	if _, ok := kubernetes["enabled"]; ok {
		t.Errorf("effective config retained legacy kubernetes.enabled:\n%s", snapshot.EffectiveYAML)
	}

	if _, err := Parse([]byte("kubernetes:\n  access_mode: file\n")); err == nil {
		t.Fatal("Parse() error = nil, want kubeconfig_path required for file mode")
	}
}

func TestDefaultYAMLUsesTwoSpacesAndEndsWithKeybindings(t *testing.T) {
	for _, line := range strings.Split(DefaultYAML, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.Contains(line[:indent], "\t") || indent%2 != 0 {
			t.Errorf("default YAML does not use two-space indentation: %q", line)
		}
	}
	if got := lastTopLevelKey(DefaultYAML); got != "keybindings:" {
		t.Errorf("default YAML ends with top-level key %q, want keybindings", got)
	}
	effective, err := Merge(nil)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if got := lastTopLevelKey(string(effective.EffectiveYAML)); got != "keybindings:" {
		t.Errorf("effective YAML ends with top-level key %q, want keybindings", got)
	}
	for _, line := range strings.Split(string(effective.EffectiveYAML), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.Contains(line[:indent], "\t") || indent%2 != 0 {
			t.Errorf("effective YAML does not use two-space indentation: %q", line)
		}
	}
}

func TestLoadRejectsDuplicateShortcutBindings(t *testing.T) {
	_, err := Parse([]byte("keybindings:\n  options: ctrl+r\n  toggle_replicas: ctrl+r\n"))
	if err == nil {
		t.Fatal("Parse() error = nil, want duplicate binding error")
	}
	if !strings.Contains(err.Error(), "duplicate shortcut binding") {
		t.Errorf("error = %q, want an actionable duplicate message", err)
	}
}

func TestDefaultKeybindingsAndViewDefaults(t *testing.T) {
	cfg := Default()
	if got := cfg.Keybindings["options"]; got != "ctrl+p" {
		t.Errorf("options binding = %q, want ctrl+p", got)
	}
	if got := cfg.Keybindings["toggle_replicas"]; got != "ctrl+t" {
		t.Errorf("toggle replicas binding = %q, want ctrl+t", got)
	}
	if _, ok := cfg.Keybindings["toggle_header"]; ok {
		t.Error("toggle_header should not be configurable after its removal")
	}
	if cfg.View.Fullscreen {
		t.Error("view.fullscreen default = true, want false")
	}
}

func TestMergeRemovesColumnsAndMigratesLegacyBindings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	snapshot, err := Merge([]byte(`columns:
  visible: [K01, K02]
keybindings:
  options: ctrl+o
  toggle_replicas: ctrl+p
  select_previous_column: shift+alt+left
  select_next_column: alt+l
  sort_ascending: alt+j
  sort_descending: shift+alt+d
  column_toggle_1: alt+1
`))
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	values, err := decodeMap(snapshot.EffectiveYAML)
	if err != nil {
		t.Fatalf("decode effective config: %v", err)
	}
	if _, ok := values["columns"]; ok {
		t.Errorf("effective config still contains columns:\n%s", snapshot.EffectiveYAML)
	}
	if bytes.Contains(snapshot.EffectiveYAML, []byte("column_toggle_")) {
		t.Errorf("effective config still contains column toggle bindings:\n%s", snapshot.EffectiveYAML)
	}
	for id, want := range map[string]string{
		"options":                "ctrl+p",
		"toggle_replicas":        "ctrl+t",
		"select_previous_column": "H",
		"select_next_column":     "L",
		"sort_ascending":         "<",
		"sort_descending":        ">",
	} {
		if got := snapshot.Config.Keybindings[id]; got != want {
			t.Errorf("keybinding %s = %q, want %q", id, got, want)
		}
	}
}

func TestPrometheusAndResourceDefaults(t *testing.T) {
	cfg := Default()
	if !cfg.Prometheus.Enabled || cfg.Prometheus.URL != "" {
		t.Errorf("prometheus defaults = %+v", cfg.Prometheus)
	}
	if cfg.Prometheus.SSLVerify {
		t.Error("prometheus.ssl_verify default = true, want false")
	}
	if cfg.Prometheus.ConnectionTimeout != 10*time.Second || cfg.Prometheus.QueryRange != 24*time.Hour {
		t.Errorf("prometheus timeouts = %v/%v", cfg.Prometheus.ConnectionTimeout, cfg.Prometheus.QueryRange)
	}
	if cfg.Prometheus.RateWindow != 5*time.Minute {
		t.Errorf("prometheus rate window = %v, want 5m", cfg.Prometheus.RateWindow)
	}
	cpu := cfg.Resources.CPU
	if !cpu.Enabled || cpu.Percentiles.Request != 90 || cpu.Percentiles.Limit != 0 ||
		cpu.SuggestionBufferPercentage.Request != 10 || cpu.RateWindow != 5*time.Minute || cpu.QueryStep != "auto" {
		t.Errorf("cpu defaults = %+v", cpu)
	}
	if cpu.SuggestionMinimum.Request != "10m" || cpu.SuggestionMinimum.Limit != "10m" {
		t.Errorf("cpu suggestion minimum = %+v", cpu.SuggestionMinimum)
	}
	memory := cfg.Resources.Memory
	if !memory.Enabled || !memory.OOMKilledCheck || memory.Percentiles.Request != 90 ||
		memory.Percentiles.Limit != 99 || memory.SuggestionBufferPercentage.Limit != 10 ||
		memory.RateWindow != 5*time.Minute {
		t.Errorf("memory defaults = %+v", memory)
	}
	if memory.SuggestionMinimum.Request != "32Mi" || memory.SuggestionMinimum.Limit != "32Mi" {
		t.Errorf("memory suggestion minimum = %+v", memory.SuggestionMinimum)
	}
}

func TestPrometheusValidation(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"high percentile", "resources:\n  cpu:\n    percentiles:\n      request: 101\n", "resources.cpu.percentiles.request"},
		{"negative buffer", "resources:\n  memory:\n    suggestion_buffer_percentage:\n      limit: -1\n", "resources.memory.suggestion_buffer_percentage.limit"},
		{"negative range", "resources:\n  memory:\n    range: -1h\n", "resources.memory.range"},
		{"bad step", "resources:\n  cpu:\n    query_step: nope\n", "resources.cpu.query_step"},
		{"bad cpu minimum", "resources:\n  cpu:\n    suggestion_minimum:\n      request: nope\n", "resources.cpu.suggestion_minimum.request"},
		{"negative memory minimum", "resources:\n  memory:\n    suggestion_minimum:\n      limit: -1Mi\n", "resources.memory.suggestion_minimum.limit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.raw))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestMovedPrometheusKeysAreIgnoredAndCaptured(t *testing.T) {
	snapshot, err := Merge([]byte("prometheus:\n  url: https://user:pass@prom.example\n  ssl_verify: true\n  tenant_header: tenant-a\n  bearer_token_env: PROM_TOKEN\n  label_filters:\n    cluster: prod\n"))
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if snapshot.Config.Prometheus.URL != "" || snapshot.Config.Prometheus.SSLVerify {
		t.Errorf("moved settings stayed in global config: %+v", snapshot.Config.Prometheus)
	}
	if snapshot.Config.Prometheus.TenantHeader != "" || snapshot.Config.Prometheus.BearerTokenEnv != "" || len(snapshot.Config.Prometheus.LabelFilters) != 0 {
		t.Errorf("moved settings stayed in global config: %+v", snapshot.Config.Prometheus)
	}
	if snapshot.LegacyPrometheus == nil || snapshot.LegacyPrometheus.URL == "" ||
		snapshot.LegacyPrometheus.SSLVerify == nil || !*snapshot.LegacyPrometheus.SSLVerify ||
		snapshot.LegacyPrometheus.TenantHeader != "tenant-a" ||
		snapshot.LegacyPrometheus.BearerTokenEnv != "PROM_TOKEN" ||
		snapshot.LegacyPrometheus.LabelFilters["cluster"] != "prod" {
		t.Errorf("legacy moved settings were not captured: %#v", snapshot.LegacyPrometheus)
	}
	if len(snapshot.Warnings) != 1 || !strings.Contains(snapshot.Warnings[0], "now live per context") {
		t.Errorf("warnings = %v, want a moved-settings warning", snapshot.Warnings)
	}
	values, err := decodeMap(snapshot.EffectiveYAML)
	if err != nil {
		t.Fatalf("decode effective config: %v", err)
	}
	prometheus, _ := values["prometheus"].(map[string]any)
	for _, key := range []string{"url", "ssl_verify", "tenant_header", "bearer_token_env", "label_filters"} {
		if _, ok := prometheus[key]; ok {
			t.Errorf("effective config retained moved prometheus key %q", key)
		}
	}
}

func TestPrometheusContextValidation(t *testing.T) {
	if err := ValidatePrometheusURL("ftp://example"); err == nil {
		t.Error("ValidatePrometheusURL(ftp) error = nil")
	}
	if err := ValidatePrometheusURL("https://user:pass@example"); err == nil {
		t.Error("ValidatePrometheusURL(userinfo) error = nil")
	}
	if err := ValidatePrometheusURL("https://prom.example"); err != nil {
		t.Errorf("ValidatePrometheusURL(valid) error = %v", err)
	}
	if err := ValidateEnvironmentName("1BAD"); err == nil {
		t.Error("ValidateEnvironmentName(1BAD) error = nil")
	}
	if _, err := ParseLabelFilters("cluster=prod, app=api"); err != nil {
		t.Errorf("ParseLabelFilters() error = %v", err)
	}
	if _, err := ParseLabelFilters("bad"); err == nil {
		t.Error("ParseLabelFilters(bad) error = nil")
	}
	if _, err := ParseLabelFilters("bad-name=value"); err == nil {
		t.Error("ParseLabelFilters(bad-name) error = nil")
	}
}

func TestApplyPrometheusOverride(t *testing.T) {
	cfg := Default()
	verify := true
	cfg.ApplyPrometheusOverride(PrometheusOverride{
		URL:            "https://prom.example",
		SSLVerify:      &verify,
		TenantHeader:   "tenant-a",
		BearerTokenEnv: "PROM_TOKEN",
		LabelFilters:   map[string]string{"cluster": "prod"},
	})
	if cfg.Prometheus.URL != "https://prom.example" || !cfg.Prometheus.SSLVerify ||
		cfg.Prometheus.TenantHeader != "tenant-a" || cfg.Prometheus.BearerTokenEnv != "PROM_TOKEN" ||
		cfg.Prometheus.LabelFilters["cluster"] != "prod" {
		t.Errorf("override not applied: %+v", cfg.Prometheus)
	}
	if cfg.Prometheus.ConnectionTimeout != 10*time.Second {
		t.Errorf("global connection timeout was overridden: %v", cfg.Prometheus.ConnectionTimeout)
	}
}

func TestRateWindowDefaultsFromPrometheus(t *testing.T) {
	cfg, err := Parse([]byte("prometheus:\n  rate_window: 7m\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.Prometheus.RateWindow != 7*time.Minute || cfg.Resources.CPU.RateWindow != 7*time.Minute || cfg.Resources.Memory.RateWindow != 7*time.Minute {
		t.Errorf("rate windows = prom %v cpu %v mem %v, want 7m", cfg.Prometheus.RateWindow, cfg.Resources.CPU.RateWindow, cfg.Resources.Memory.RateWindow)
	}
}

func TestRequestTimeoutAcceptsSecondsAndDurations(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"6":     6 * time.Second,
		"15":    15 * time.Second,
		"6s":    6 * time.Second,
		"1m":    time.Minute,
		"500ms": 500 * time.Millisecond,
	} {
		cfg, err := Parse([]byte("kubernetes:\n  request_timeout: " + raw + "\n"))
		if err != nil {
			t.Errorf("request_timeout %q: %v", raw, err)
			continue
		}
		if got := time.Duration(cfg.Kubernetes.RequestTimeout); got != want {
			t.Errorf("request_timeout %q = %v, want %v", raw, got, want)
		}
	}
	if _, err := Parse([]byte("kubernetes:\n  request_timeout: soon\n")); err == nil {
		t.Error("request_timeout \"soon\" should be rejected")
	}
}

func TestLoadAcceptsEmptyAndPartialKeybindings(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":           "",
		"comments only":   "# nothing configured\n",
		"empty section":   "keybindings:\n",
		"one binding":     "keybindings:\n  fullscreen: F\n",
		"unrelated block": "skin:\n  body:\n    bgColor: \"#101010\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			useTempConfigHome(t)
			path, err := EffectivePath()
			if err != nil {
				t.Fatal(err)
			}
			writeConfigFile(t, path, raw)
			loaded, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got := loaded.Config.Keybindings["sort_ascending"]; got != "<" {
				t.Errorf("sort_ascending = %q, want default <", got)
			}
			if time.Duration(loaded.Config.Kubernetes.RequestTimeout) != 6*time.Second {
				t.Error("kubernetes.request_timeout default was lost")
			}
		})
	}
}

func TestDefaultViewSortsByNamespace(t *testing.T) {
	cfg := Default()
	if cfg.View.SortColumn != "K02" || cfg.View.SortDescending {
		t.Errorf("default sort = %q descending=%v, want K02 ascending", cfg.View.SortColumn, cfg.View.SortDescending)
	}
}
