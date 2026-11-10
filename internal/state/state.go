// Package state persists per-cluster view state under the XDG configuration
// directory.
package state

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// StateSchemaVersion is the current state.yaml schema. Version 0 is the
// implicit, pre-versioning layout.
const StateSchemaVersion = 1

// State is the persisted view state for one cluster and context. Prometheus
// URL, TLS, tenant, authentication environment-variable names, and label
// filters are context-specific and live here rather than in config.yaml.
type State struct {
	SchemaVersion    int      `yaml:"schema_version,omitempty"`
	Namespaces       []string `yaml:"namespaces"`
	ActiveNamespace  string   `yaml:"active_namespace"`
	Columns          []string `yaml:"columns"`
	ReplicaCollapsed *bool    `yaml:"replica_collapsed,omitempty"`
	HiddenRows       []string `yaml:"hidden_rows"`
	Fullscreen       bool     `yaml:"fullscreen"`
	SortColumn       string   `yaml:"sort_column,omitempty"`
	SortDescending   bool     `yaml:"sort_descending,omitempty"`

	PrometheusURL              string            `yaml:"prometheus_url,omitempty"`
	PrometheusSSLVerify        *bool             `yaml:"prometheus_ssl_verify,omitempty"`
	PrometheusTenantHeader     string            `yaml:"prometheus_tenant_header,omitempty"`
	PrometheusBearerTokenEnv   string            `yaml:"prometheus_bearer_token_env,omitempty"`
	PrometheusBasicUsernameEnv string            `yaml:"prometheus_basic_username_env,omitempty"`
	PrometheusBasicPasswordEnv string            `yaml:"prometheus_basic_password_env,omitempty"`
	PrometheusLabelFilters     map[string]string `yaml:"prometheus_label_filters,omitempty"`

	QColumnsCustomized bool `yaml:"q_columns_customized,omitempty"`
}

// Dir returns the state directory for a cluster. It is kept for compatibility
// with context-less callers and existing state paths.
func Dir(cluster string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve XDG config directory: %w", err)
	}
	return filepath.Join(base, "kubernova", "clusters", safeName(cluster)), nil
}

// DirFor returns the state directory for a cluster and Kubernetes context.
// Context is part of the identity because it determines the API target.
func DirFor(cluster, context string) (string, error) {
	if strings.TrimSpace(context) == "" {
		return Dir(cluster)
	}
	dir, err := Dir(cluster)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, safeName(context)), nil
}

// Path returns the state file path for a cluster.
func Path(cluster string) (string, error) {
	dir, err := Dir(cluster)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.yaml"), nil
}

// PathFor returns the state file path for a cluster and context.
func PathFor(cluster, context string) (string, error) {
	dir, err := DirFor(cluster, context)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.yaml"), nil
}

// Load reads a cluster's state. A missing file returns an empty state.
func Load(cluster string) (State, error) {
	value, err := loadPath(cluster, "")
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	return value, err
}

// LoadFor reads a context-specific state and falls back to the legacy
// cluster-only path when no context-specific state exists yet.
func LoadFor(cluster, context string) (State, error) {
	value, err := loadPath(cluster, context)
	if errors.Is(err, fs.ErrNotExist) && strings.TrimSpace(context) != "" {
		return Load(cluster)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	return value, err
}

func loadPath(cluster, context string) (State, error) {
	var (
		path string
		err  error
	)
	if strings.TrimSpace(context) == "" {
		path, err = Path(cluster)
	} else {
		path, err = PathFor(cluster, context)
	}
	if err != nil {
		return State{}, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, err
	}
	if err != nil {
		return State{}, fmt.Errorf("read cluster state %q: %w", path, err)
	}

	var value State
	if err := yaml.Unmarshal(raw, &value); err != nil {
		return State{}, fmt.Errorf("parse cluster state %q: %w", path, err)
	}
	migrate(&value)
	return value, nil
}

// migrate upgrades old implicit schemas in memory. The next save persists the
// current version.
func migrate(value *State) {
	if value.SchemaVersion < StateSchemaVersion {
		if value.PrometheusLabelFilters == nil {
			value.PrometheusLabelFilters = make(map[string]string)
		}
		value.SchemaVersion = StateSchemaVersion
	}
}

// Save writes a cluster's state atomically.
func Save(cluster string, value State) error {
	return SaveFor(cluster, "", value)
}

// SaveFor writes context-specific state atomically.
func SaveFor(cluster, context string, value State) error {
	var (
		path string
		err  error
	)
	if strings.TrimSpace(context) == "" {
		path, err = Path(cluster)
	} else {
		path, err = PathFor(cluster, context)
	}
	if err != nil {
		return err
	}
	return savePath(path, value)
}

func savePath(path string, value State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create cluster state directory: %w", err)
	}
	value.SchemaVersion = StateSchemaVersion
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode cluster state: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("encode cluster state: %w", err)
	}
	raw := buffer.Bytes()

	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return fmt.Errorf("write cluster state %q: %w", temporary, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace cluster state %q: %w", path, err)
	}
	return nil
}

// ExistsFor reports whether context-specific or legacy cluster state exists.
func ExistsFor(cluster, context string) bool {
	if strings.TrimSpace(context) != "" {
		if path, err := PathFor(cluster, context); err == nil {
			if _, err := os.Stat(path); err == nil {
				return true
			}
		}
	}
	path, err := Path(cluster)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func safeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "._")
	if name == "" {
		return "default"
	}
	return name
}
