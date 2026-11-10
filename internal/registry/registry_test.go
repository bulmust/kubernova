package registry

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultBindingsAreUniqueAndLowercase(t *testing.T) {
	reg := Default()
	if err := reg.ValidateBindings(); err != nil {
		t.Fatalf("Default().ValidateBindings() error = %v", err)
	}
	if got := reg.Key(ShortcutOptions); got != "ctrl+p" {
		t.Errorf("options binding = %q, want ctrl+p", got)
	}
	if len(reg.Shortcuts) == 0 || reg.Shortcuts[0].ID != ShortcutRefresh {
		t.Error("refresh must be the first shortcut in the header registry order")
	}
	if got := reg.Key(ShortcutToggleReplicas); got != "ctrl+t" {
		t.Errorf("toggle replicas binding = %q, want ctrl+t", got)
	}
	for id, want := range map[string]string{
		ShortcutSelectLeft:     "H",
		ShortcutSelectRight:    "L",
		ShortcutSortAscending:  "<",
		ShortcutSortDescending: ">",
	} {
		if got := reg.Key(id); got != want {
			t.Errorf("%s binding = %q, want %q", id, got, want)
		}
	}
	for _, shortcut := range reg.Shortcuts {
		if len([]rune(shortcut.Key)) > 1 && shortcut.Key != strings.ToLower(shortcut.Key) {
			t.Errorf("shortcut %q key %q is not lowercase", shortcut.ID, shortcut.Key)
		}
		if shortcut.Description != strings.ToLower(shortcut.Description) {
			t.Errorf("shortcut %q description %q is not lowercase", shortcut.ID, shortcut.Description)
		}
	}
	for _, command := range reg.Commands {
		if command.Name != strings.ToLower(command.Name) {
			t.Errorf("command name %q is not lowercase", command.Name)
		}
	}
}

func TestWithBindingsAndDuplicateValidation(t *testing.T) {
	reg := Default().WithBindings(map[string]string{
		ShortcutOptions: "ctrl+x",
	})
	if got := reg.Key(ShortcutOptions); got != "ctrl+x" {
		t.Errorf("options binding = %q, want ctrl+x", got)
	}

	duplicate := Default().WithBindings(map[string]string{
		ShortcutOptions:        "ctrl+r",
		ShortcutToggleReplicas: "ctrl+r",
	})
	if err := duplicate.ValidateBindings(); err == nil {
		t.Fatal("ValidateBindings() error = nil, want duplicate binding error")
	} else if !strings.Contains(err.Error(), "duplicate shortcut binding") {
		t.Errorf("error = %q, want an actionable duplicate message", err)
	}
}

func TestCommandAliasResolution(t *testing.T) {
	reg := Default()
	for input, want := range map[string]string{
		"promurl":       CommandPrometheusURL,
		"prometheusurl": CommandPrometheusURL,
		"namespace":     CommandNamespace,
		"options":       CommandOptions,
		"q":             CommandQuit,
		"c":             CommandColumns,
		"reload":        CommandReload,
		"status":        CommandStatus,
	} {
		command, ok := reg.Command(input)
		if !ok {
			t.Fatalf("Command(%q) not found", input)
		}
		if command.Name != want {
			t.Errorf("Command(%q).Name = %q, want %q", input, command.Name, want)
		}
	}
}

func TestCommandsAndAliasesAreCollisionFree(t *testing.T) {
	reg := Default()
	seen := make(map[string]string)
	for _, command := range reg.Commands {
		for _, name := range append([]string{command.Name}, command.Aliases...) {
			if other, ok := seen[name]; ok {
				t.Errorf("command name %q is used by %q and %q", name, other, command.Name)
			}
			seen[name] = command.Name
		}
	}
	refresh, ok := reg.Command(CommandRefresh)
	if !ok || len(refresh.Aliases) != 0 {
		t.Errorf("refresh should not alias another command: %#v", refresh)
	}
	reload, ok := reg.Command(CommandReload)
	if !ok || reload.Name != CommandReload {
		t.Errorf("reload command = %#v, %v", reload, ok)
	}
	if command, ok := reg.Command("c"); !ok || command.Name != CommandColumns {
		t.Errorf("c alias = %#v, %v; want columns", command, ok)
	}
}

func TestSingleCharacterBindingsKeepCase(t *testing.T) {
	if got := NormalizeKey(" H "); got != "H" {
		t.Errorf("NormalizeKey(H) = %q, want H", got)
	}
	if got := NormalizeKey("Ctrl-P"); got != "ctrl+p" {
		t.Errorf("NormalizeKey(Ctrl-P) = %q, want ctrl+p", got)
	}
	reg := Default().WithBindings(map[string]string{ShortcutMoveDown: "h"})
	if err := reg.ValidateBindings(); err != nil {
		t.Errorf("h and H are distinct bindings, got %v", err)
	}
}

func TestValidateBindingsRejectsReservedKeys(t *testing.T) {
	for _, key := range []string{"1", "g", "esc", "tab", "ctrl+c"} {
		reg := Default().WithBindings(map[string]string{ShortcutFullscreen: key})
		if err := reg.ValidateBindings(); err == nil {
			t.Errorf("binding %q should be rejected as reserved", key)
		}
	}
}

func TestDefaultBindingsDoNotShadowFixedKeys(t *testing.T) {
	reg := Default()
	for _, fixed := range reg.Fixed {
		for _, shortcut := range reg.Shortcuts {
			if shortcut.Key == fixed.Key {
				t.Errorf("shortcut %q collides with fixed key %q", shortcut.ID, fixed.Key)
			}
		}
	}
}

// TestKeybindingsDocMatchesRegistry keeps docs/keybindings.md in sync with the
// registry. Update the document when this test fails.
func TestKeybindingsDocMatchesRegistry(t *testing.T) {
	raw, err := os.ReadFile("../../docs/keybindings.md")
	if err != nil {
		t.Fatalf("read docs/keybindings.md: %v", err)
	}
	doc := string(raw)
	hasRow := func(cells ...string) bool {
		for _, line := range strings.Split(doc, "\n") {
			if !strings.HasPrefix(line, "|") {
				continue
			}
			matched := true
			for _, cell := range cells {
				if !strings.Contains(line, cell) {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
		}
		return false
	}
	escape := func(value string) string { return strings.ReplaceAll(value, "|", "\\|") }

	reg := Default()
	for _, shortcut := range reg.Shortcuts {
		if !hasRow("`"+shortcut.Key+"`", "`"+shortcut.ID+"`", shortcut.Description) {
			t.Errorf("docs/keybindings.md has no row for shortcut %s (%s, %q)", shortcut.ID, shortcut.Key, shortcut.Description)
		}
	}
	for _, fixed := range reg.Fixed {
		if !hasRow("`"+fixed.Key+"`", fixed.Description) {
			t.Errorf("docs/keybindings.md has no row for fixed key %q", fixed.Key)
		}
	}
	for _, command := range reg.Commands {
		cells := []string{"`:" + command.Name + "`", command.Description}
		for _, alias := range command.Aliases {
			cells = append(cells, "`:"+alias+"`")
		}
		if command.Usage != "" {
			cells = append(cells, "`"+escape(command.Usage)+"`")
		}
		if !hasRow(cells...) {
			t.Errorf("docs/keybindings.md has no row for command :%s", command.Name)
		}
	}
}
