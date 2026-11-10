// Package configdefaults exposes the documentation-rich default
// configuration files stored under config/kubernova/.
package configdefaults

import (
	"embed"
	"sort"
	"strings"
)

//go:embed *.yaml
var defaultFiles embed.FS

const generatedHeader = `# Kubernova default configuration.
# This file is generated from config/kubernova/*.yaml.
# It is documentation only; edit config.yaml to change active settings.

`

// YAML returns the combined, comment-rich default configuration.
func YAML() string {
	entries, err := defaultFiles.ReadDir(".")
	if err != nil {
		return generatedHeader
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
			names = append(names, entry.Name())
		}
	}
	// Keybindings are emitted last so the generated default configuration
	// ends with the section users edit most often.
	sort.Slice(names, func(i, j int) bool {
		if names[i] == "keybindings.yaml" {
			return false
		}
		if names[j] == "keybindings.yaml" {
			return true
		}
		return names[i] < names[j]
	})

	var b strings.Builder
	b.WriteString(generatedHeader)
	for _, name := range names {
		data, err := defaultFiles.ReadFile(name)
		if err != nil {
			continue
		}
		b.Write(data)
		if !strings.HasSuffix(string(data), "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}
