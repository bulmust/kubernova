// Package registry is the single source of truth for Kubernova commands,
// keyboard shortcuts, and their configured bindings.
package registry

import (
	"fmt"
	"sort"
	"strings"
)

// Command describes a command-bar command and its aliases.
type Command struct {
	Name        string
	Aliases     []string
	Description string
	Category    string
	// Usage is appended to the help label, for example "<url>".
	Usage string
}

// Shortcut describes a keyboard action and its configurable key binding.
type Shortcut struct {
	ID           string
	Key          string
	Description  string
	Category     string
	Configurable bool
}

// Registry bundles commands and shortcuts for one effective configuration.
type Registry struct {
	Commands  []Command
	Shortcuts []Shortcut
	// Fixed lists keys with built-in meaning that cannot be rebound. They are
	// documented in help and rejected as custom bindings.
	Fixed []Shortcut
}

// Canonical command names.
const (
	CommandQuit              = "quit"
	CommandHelp              = "help"
	CommandColumns           = "columns"
	CommandOptions           = "option"
	CommandNamespace         = "ns"
	CommandPrometheusURL     = "prometheusurl"
	CommandPrometheusConfig  = "promconfig"
	CommandPrometheusConnect = "connect"
	CommandKubeconfigPath    = "kubeconfigpath"
	CommandContext           = "context"
	CommandFullscreen        = "fullscreen"
	CommandRefresh           = "refresh"
	CommandReload            = "reload"
	CommandExport            = "export"
	CommandStatus            = "status"
)

// Canonical shortcut identifiers.
const (
	ShortcutFilter           = "filter"
	ShortcutCommand          = "command"
	ShortcutHelp             = "help"
	ShortcutColumns          = "columns"
	ShortcutOptions          = "options"
	ShortcutRefresh          = "refresh"
	ShortcutToggleReplicas   = "toggle_replicas"
	ShortcutHideRow          = "hide_row"
	ShortcutFullscreen       = "fullscreen"
	ShortcutMoveDown         = "move_down"
	ShortcutMoveUp           = "move_up"
	ShortcutNamespaceAll     = "namespace_all"
	ShortcutSelectLeft       = "select_previous_column"
	ShortcutSelectRight      = "select_next_column"
	ShortcutSortAscending    = "sort_ascending"
	ShortcutSortDescending   = "sort_descending"
	ShortcutPrometheusConfig = "prometheus_config"
	ShortcutExport           = "export"
)

// Default returns the built-in command and shortcut registry. Multi-key
// bindings are lower-case; single-character bindings keep their case, so "H"
// and "h" are different keys.
func Default() Registry {
	shortcuts := []Shortcut{
		{ID: ShortcutRefresh, Key: "ctrl+r", Description: "refresh", Category: "data", Configurable: true},
		{ID: ShortcutFilter, Key: "/", Description: "filter text", Category: "navigation", Configurable: true},
		{ID: ShortcutCommand, Key: ":", Description: "commands", Category: "navigation", Configurable: true},
		{ID: ShortcutHelp, Key: "?", Description: "open help", Category: "navigation", Configurable: true},
		{ID: ShortcutColumns, Key: "c", Description: "filter columns", Category: "view", Configurable: true},
		{ID: ShortcutOptions, Key: "ctrl+p", Description: "edit config.yaml", Category: "configuration", Configurable: true},
		{ID: ShortcutPrometheusConfig, Key: "ctrl+g", Description: "prometheus config", Category: "configuration", Configurable: true},
		{ID: ShortcutExport, Key: "ctrl+e", Description: "export report", Category: "data", Configurable: true},
		{ID: ShortcutToggleReplicas, Key: "ctrl+t", Description: "toggle replicas", Category: "view", Configurable: true},
		{ID: ShortcutHideRow, Key: "space", Description: "hide row to end", Category: "view", Configurable: true},
		{ID: ShortcutFullscreen, Key: "f", Description: "fullscreen", Category: "view", Configurable: true},
		{ID: ShortcutMoveDown, Key: "j", Description: "move down", Category: "navigation", Configurable: true},
		{ID: ShortcutMoveUp, Key: "k", Description: "move up", Category: "navigation", Configurable: true},
		{ID: ShortcutNamespaceAll, Key: "0", Description: "all namespaces", Category: "navigation", Configurable: true},
		{ID: ShortcutSelectLeft, Key: "H", Description: "select left column", Category: "view", Configurable: true},
		{ID: ShortcutSelectRight, Key: "L", Description: "select right column", Category: "view", Configurable: true},
		{ID: ShortcutSortAscending, Key: "<", Description: "sort ascending", Category: "view", Configurable: true},
		{ID: ShortcutSortDescending, Key: ">", Description: "sort descending", Category: "view", Configurable: true},
	}

	fixed := []Shortcut{
		{Key: "1-9", Description: "select namespace shortcut shown in the header", Category: "navigation"},
		{Key: "g / G", Description: "jump to first / last row", Category: "navigation"},
		{Key: "arrows, pgup, pgdn", Description: "move in the table", Category: "navigation"},
		{Key: "tab", Description: "accept inline completion", Category: "navigation"},
		{Key: "enter", Description: "confirm", Category: "navigation"},
		{Key: "esc", Description: "close, cancel, or clear the filter", Category: "navigation"},
		{Key: "ctrl+c", Description: "quit", Category: "application"},
	}

	return Registry{
		Fixed: fixed,
		Commands: []Command{
			{Name: CommandQuit, Aliases: []string{"q"}, Description: "quit", Category: "application"},
			{Name: CommandHelp, Description: "open help", Category: "navigation"},
			{Name: CommandColumns, Aliases: []string{"c"}, Description: "choose visible columns", Category: "view"},
			{Name: CommandFullscreen, Aliases: []string{"fs"}, Description: "toggle table-only fullscreen", Category: "view"},
			{Name: CommandOptions, Aliases: []string{"options"}, Description: "edit config.yaml", Category: "configuration"},
			{Name: CommandNamespace, Aliases: []string{"namespace"}, Description: "filter by namespace", Category: "navigation", Usage: "<namespace|all>"},
			{Name: CommandPrometheusURL, Aliases: []string{"promurl"}, Description: "set the Prometheus URL without connecting", Category: "configuration", Usage: "<url>"},
			{Name: CommandPrometheusConfig, Aliases: []string{"promconf"}, Description: "edit per-context Prometheus settings", Category: "configuration"},
			{Name: CommandPrometheusConnect, Aliases: []string{"prometheus"}, Description: "connect Prometheus and run resource queries", Category: "data", Usage: "[url]"},
			{Name: CommandKubeconfigPath, Aliases: []string{"kubeapipath"}, Description: "choose the kubeconfig file", Category: "configuration", Usage: "[path]"},
			{Name: CommandContext, Aliases: []string{"ctx"}, Description: "switch Kubernetes context", Category: "navigation", Usage: "<context>"},
			{Name: CommandRefresh, Description: "refresh Kubernetes data and connections", Category: "data"},
			{Name: CommandReload, Description: "reload configuration and keybindings", Category: "configuration"},
			{Name: CommandExport, Description: "export the complete report and table", Category: "data", Usage: "[json|md|all] [path]"},
			{Name: CommandStatus, Description: "show status message history", Category: "navigation"},
		},
		Shortcuts: shortcuts,
	}
}

// Command resolves a canonical name or alias.
func (r Registry) Command(name string) (Command, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, command := range r.Commands {
		if command.Name == name {
			return command, true
		}
		for _, alias := range command.Aliases {
			if strings.ToLower(alias) == name {
				return command, true
			}
		}
	}
	return Command{}, false
}

// CommandNames returns canonical names followed by aliases, sorted for stable
// completion.
func (r Registry) CommandNames() []string {
	names := make([]string, 0, len(r.Commands)*2)
	for _, command := range r.Commands {
		names = append(names, command.Name)
		names = append(names, command.Aliases...)
	}
	sort.Strings(names)
	return names
}

// Shortcut resolves a shortcut identifier.
func (r Registry) Shortcut(id string) (Shortcut, bool) {
	for _, shortcut := range r.Shortcuts {
		if shortcut.ID == id {
			return shortcut, true
		}
	}
	return Shortcut{}, false
}

// Key returns the configured key label for a shortcut identifier.
func (r Registry) Key(id string) string {
	if shortcut, ok := r.Shortcut(id); ok {
		return shortcut.Key
	}
	return ""
}

// BindingDefaults returns the configurable binding defaults keyed by shortcut
// identifier.
func (r Registry) BindingDefaults() map[string]string {
	defaults := make(map[string]string)
	for _, shortcut := range r.Shortcuts {
		if shortcut.Configurable {
			defaults[shortcut.ID] = shortcut.Key
		}
	}
	return defaults
}

// WithBindings returns a copy with configured bindings applied. Missing or
// empty bindings keep the built-in default.
func (r Registry) WithBindings(bindings map[string]string) Registry {
	next := Registry{
		Commands:  append([]Command(nil), r.Commands...),
		Shortcuts: append([]Shortcut(nil), r.Shortcuts...),
		Fixed:     append([]Shortcut(nil), r.Fixed...),
	}
	for index := range next.Shortcuts {
		shortcut := &next.Shortcuts[index]
		if !shortcut.Configurable {
			continue
		}
		if value := NormalizeKey(bindings[shortcut.ID]); value != "" {
			shortcut.Key = value
		} else {
			shortcut.Key = NormalizeKey(shortcut.Key)
		}
	}
	return next
}

// ValidateBindings rejects duplicate shortcut bindings with an actionable
// error naming the conflicting shortcut identifiers.
func (r Registry) ValidateBindings() error {
	seen := make(map[string]string)
	for _, shortcut := range r.Shortcuts {
		if !shortcut.Configurable {
			continue
		}
		key := NormalizeKey(shortcut.Key)
		if key == "" {
			return fmt.Errorf("shortcut %q has an empty binding", shortcut.ID)
		}
		if isReservedKey(key) {
			return fmt.Errorf("shortcut %q cannot use %q; it has a fixed meaning", shortcut.ID, key)
		}
		if other, ok := seen[key]; ok {
			return fmt.Errorf(
				"duplicate shortcut binding %q is used by %q and %q; change one of them in keybindings",
				key, other, shortcut.ID,
			)
		}
		seen[key] = shortcut.ID
	}
	return nil
}

// isReservedKey reports whether a normalized key has a fixed meaning.
func isReservedKey(key string) bool {
	switch key {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9",
		"g", "G", "esc", "escape", "tab", "enter", "ctrl+c",
		"up", "down", "left", "right", "pgup", "pgdn", "home", "end":
		return true
	}
	return false
}

// NormalizeKey canonicalizes a key binding for comparison and display. A
// single character keeps its case; everything else is lower-cased.
func NormalizeKey(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) == 1 {
		return value
	}
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "-", "+")
	return value
}
