package ui

import (
	"strings"
	"testing"

	"github.com/rivo/tview"

	"kubernova/internal/registry"
)

func helpText(t *testing.T) string {
	t.Helper()
	return helpViewText(NewHelp(registry.Default()))
}

func helpViewText(help *Help) string {
	var b strings.Builder
	for index := 0; index < help.GetItemCount(); index++ {
		if view, ok := help.GetItem(index).(*tview.TextView); ok {
			b.WriteString(view.GetText(true))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func TestHelpUsesRegistryAndSharedDescriptions(t *testing.T) {
	help := NewHelp(registry.Default())
	if got := help.GetItemCount(); got != 2 {
		t.Errorf("help item count = %d, want commands and shortcuts only", got)
	}
	text := helpText(t)
	for _, want := range []string{
		":columns, :c",
		":prometheusurl, :promurl <url>",
		":option, :options",
		"open help",
		"<ctrl+p>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("help text does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "(alias:") {
		t.Errorf("help text still uses the old alias format:\n%s", text)
	}
	if strings.Contains(text, "COLUMN IDENTIFIERS") || strings.Contains(text, "K01") {
		t.Errorf("help text still contains the columns pane:\n%s", text)
	}
}

func TestHelpUpdateReflectsEditedBindings(t *testing.T) {
	help := NewHelp(registry.Default())
	updated := registry.Default().WithBindings(map[string]string{registry.ShortcutOptions: "ctrl+x"})
	help.Update(updated)
	text := helpViewText(help)
	if !strings.Contains(text, "<ctrl+x>") {
		t.Errorf("help text did not pick up the edited binding:\n%s", text)
	}
	if strings.Contains(text, "<ctrl+p>") {
		t.Errorf("help text kept the old binding:\n%s", text)
	}
}

func TestHelpListsEveryShortcutAndFixedKey(t *testing.T) {
	reg := registry.Default()
	text := helpViewText(NewHelp(reg))
	for _, shortcut := range append(append([]registry.Shortcut{}, reg.Shortcuts...), reg.Fixed...) {
		if !strings.Contains(text, "<"+shortcut.Key+">") || !strings.Contains(text, shortcut.Description) {
			t.Errorf("help does not list %q (%s)", shortcut.Key, shortcut.Description)
		}
	}
}
