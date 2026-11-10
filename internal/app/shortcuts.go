package app

import (
	"kubernova/internal/registry"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// matchShortcut reports whether an event matches a configured binding. Bindings
// use lower-case names such as "ctrl+p", "ctrl+j", or "space".
func matchShortcut(ev *tcell.EventKey, binding string) bool {
	binding = registry.NormalizeKey(binding)
	if binding == "" || ev == nil {
		return false
	}
	parts := strings.Split(binding, "+")
	name := parts[len(parts)-1]
	wantCtrl, wantAlt, wantShift := false, false, false
	for _, modifier := range parts[:len(parts)-1] {
		switch modifier {
		case "ctrl":
			wantCtrl = true
		case "alt":
			wantAlt = true
		case "shift":
			wantShift = true
		}
	}
	modifiers := ev.Modifiers()

	if wantCtrl {
		key, ok := ctrlKey(name)
		return ok && ev.Key() == key && modifiers&tcell.ModAlt == 0
	}

	if wantAlt {
		if modifiers&tcell.ModAlt == 0 {
			return false
		}
		if wantShift && modifiers&tcell.ModShift == 0 {
			return false
		}
		switch name {
		case "left":
			return ev.Key() == tcell.KeyLeft
		case "right":
			return ev.Key() == tcell.KeyRight
		case "up":
			return ev.Key() == tcell.KeyUp
		case "down":
			return ev.Key() == tcell.KeyDown
		default:
			return ev.Key() == tcell.KeyRune && strings.EqualFold(string(ev.Rune()), name)
		}
	}

	if wantShift {
		if modifiers&tcell.ModShift == 0 {
			return false
		}
		return ev.Key() == tcell.KeyRune && strings.EqualFold(string(ev.Rune()), name)
	}

	if name == "space" {
		return ev.Key() == tcell.KeyRune && ev.Rune() == ' ' && modifiers&(tcell.ModAlt|tcell.ModCtrl) == 0
	}
	if runes := []rune(name); len(runes) == 1 {
		return ev.Key() == tcell.KeyRune &&
			ev.Rune() == runes[0] &&
			modifiers&(tcell.ModAlt|tcell.ModCtrl) == 0
	}
	return false
}

func ctrlKey(name string) (tcell.Key, bool) {
	if name == "space" {
		return tcell.KeyCtrlSpace, true
	}
	runes := []rune(name)
	if len(runes) != 1 || runes[0] < 'a' || runes[0] > 'z' {
		return 0, false
	}
	return tcell.KeyCtrlA + tcell.Key(runes[0]-'a'), true
}
