package app

import (
	"kubernova/internal/registry"
	"sort"
	"strings"
)

// commandCompletions returns full command-bar text entries for command names,
// aliases, and namespace values.
func commandCompletions(reg registry.Registry, currentText string, namespaces []string, contextLists ...[]string) []string {
	contexts := make([]string, 0)
	if len(contextLists) > 0 {
		contexts = append(contexts, contextLists[0]...)
	}
	text := strings.TrimSpace(currentText)
	if text == "" {
		return nil
	}
	fields := strings.Fields(text)
	// An exact command or alias wins over prefix completion. This keeps short
	// aliases such as :c deterministic even when longer commands share the
	// prefix.
	if len(fields) == 1 && !strings.HasSuffix(currentText, " ") {
		if _, ok := reg.Command(fields[0]); ok {
			return []string{fields[0]}
		}
	}
	command, commandOK := reg.Command(fields[0])
	if commandOK && command.Name == registry.CommandPrometheusConnect && !strings.EqualFold(fields[0], "prometheus") {
		query := ""
		if len(fields) >= 2 {
			query = strings.ToLower(fields[1])
		} else if !strings.HasSuffix(currentText, " ") {
			query = strings.ToLower(fields[0])
			if !strings.HasPrefix("connect", query) {
				return nil
			}
		}
		if strings.HasPrefix("prometheus", query) {
			return []string{"connect prometheus"}
		}
		return nil
	}
	argumentMode := len(fields) >= 2
	if len(fields) == 1 && strings.HasSuffix(currentText, " ") {
		argumentMode = true
	}
	if argumentMode {
		command, ok := reg.Command(fields[0])
		if !ok {
			return nil
		}
		prefix := fields[0] + " "
		query := ""
		if len(fields) >= 2 {
			query = strings.ToLower(fields[1])
		}
		var candidates []string
		switch command.Name {
		case registry.CommandNamespace:
			candidates = append([]string{"all"}, namespaces...)
		case registry.CommandContext:
			candidates = contexts
		case registry.CommandExport:
			candidates = []string{"json", "md", "all"}
		default:
			return nil
		}
		entries := make([]string, 0, len(candidates))
		seen := make(map[string]bool)
		for _, candidate := range candidates {
			if candidate == "" || seen[candidate] || !strings.HasPrefix(strings.ToLower(candidate), query) {
				continue
			}
			seen[candidate] = true
			entries = append(entries, prefix+candidate)
		}
		sort.Strings(entries)
		return entries
	}

	query := strings.ToLower(fields[0])
	entries := make([]string, 0)
	seen := make(map[string]bool)
	for _, name := range reg.CommandNames() {
		if !strings.HasPrefix(strings.ToLower(name), query) || seen[name] {
			continue
		}
		seen[name] = true
		entries = append(entries, name)
	}
	sort.Strings(entries)
	return entries
}

// completionSuffix returns the remaining text of the first candidate that
// extends the current input.
func completionSuffix(currentText string, candidates []string) string {
	if currentText == "" || len(candidates) == 0 {
		return ""
	}
	lower := strings.ToLower(currentText)
	for _, candidate := range candidates {
		if candidate == currentText {
			continue
		}
		if strings.HasPrefix(strings.ToLower(candidate), lower) {
			return candidate[len(currentText):]
		}
	}
	return ""
}
