package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"kubernova/internal/registry"
)

// Help is the help page: commands and shortcuts.
type Help struct {
	*tview.Flex

	commands  *tview.TextView
	shortcuts *tview.TextView
}

// NewHelp creates the help page. When a registry is supplied, commands,
// aliases, shortcuts, and descriptions come from that single source of truth.
func NewHelp(registries ...registry.Registry) *Help {
	reg := registry.Default()
	if len(registries) > 0 {
		reg = registries[0]
	}

	help := &Help{
		Flex:      tview.NewFlex().SetDirection(tview.FlexColumn),
		commands:  newHelpView(" COMMANDS "),
		shortcuts: newHelpView(" SHORTCUTS "),
	}
	help.
		AddItem(help.commands, 0, 1, true).
		AddItem(help.shortcuts, 0, 1, true)
	help.SetBackgroundColor(BackgroundColor)
	help.Update(reg)
	return help
}

// Update rebuilds the help text from the active registry so edited shortcuts
// and aliases take effect without restarting.
func (h *Help) Update(reg registry.Registry) {
	var commandText strings.Builder
	commandText.WriteString(" [yellow::b]COMMAND BAR[-:-:-]")
	for _, command := range reg.Commands {
		label := ":" + command.Name
		for _, alias := range command.Aliases {
			label += ", :" + alias
		}
		if command.Usage != "" {
			label += " " + command.Usage
		}
		fmt.Fprintf(
			&commandText,
			"\n [yellow]%-42s[-] %s",
			tview.Escape(label),
			tview.Escape(command.Description),
		)
	}
	h.commands.SetText(commandText.String())

	var shortcutText strings.Builder
	shortcutText.WriteString(" [yellow::b]KEYBOARD[-:-:-]")
	for _, shortcut := range reg.Shortcuts {
		fmt.Fprintf(
			&shortcutText,
			"\n [yellow]<%s>[-] %s",
			tview.Escape(shortcut.Key),
			tview.Escape(shortcut.Description),
		)
	}
	if len(reg.Fixed) > 0 {
		shortcutText.WriteString("\n\n [yellow::b]FIXED KEYS[-:-:-]")
		for _, shortcut := range reg.Fixed {
			fmt.Fprintf(
				&shortcutText,
				"\n [yellow]<%s>[-] %s",
				tview.Escape(shortcut.Key),
				tview.Escape(shortcut.Description),
			)
		}
	}
	h.shortcuts.SetText(shortcutText.String())
}

func newHelpView(title string) *tview.TextView {
	view := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true).
		SetTextStyle(tcell.StyleDefault.
			Background(BackgroundColor).
			Foreground(tcell.ColorWhite))
	view.SetBackgroundColor(BackgroundColor)
	view.SetBorder(true)
	view.SetBorderColor(tcell.ColorFuchsia)
	view.SetTitle(title)
	view.SetTitleColor(tcell.ColorFuchsia)
	return view
}
