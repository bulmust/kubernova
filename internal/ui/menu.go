package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// MenuHint is one keyboard hint rendered in the header menu.
type MenuHint struct {
	Key         string
	Description string
	Digit       bool
	Danger      bool
}

const menuMaxRows = 6

// Menu renders the active view's keyboard hints.
type Menu struct {
	*tview.TextView

	hints []MenuHint
	skin  Skin
}

// NewMenu creates the middle header column and subscribes it to skin changes.
func NewMenu(manager *SkinManager) *Menu {
	menu := &Menu{
		TextView: tview.NewTextView().
			SetDynamicColors(true).
			SetWrap(false),
	}
	manager.Subscribe(menu.ApplySkin)
	return menu
}

// SetHints replaces the menu hints and rebuilds the layout.
func (m *Menu) SetHints(hints []MenuHint) {
	m.hints = append([]MenuHint(nil), hints...)
	m.render()
}

// ApplySkin applies a new skin and repaints.
func (m *Menu) ApplySkin(skin Skin) {
	m.skin = skin
	m.render()
}

type menuColumn struct {
	hints    []MenuHint
	keyWidth int
	width    int
	digit    bool
}

func (m *Menu) render() {
	skin := m.skin
	m.SetBackgroundColor(skin.BodyBG)
	m.SetTextStyle(tcell.StyleDefault.
		Background(skin.BodyBG).
		Foreground(skin.MenuFG))

	columns := m.columns()
	if len(columns) == 0 {
		m.SetText("")
		return
	}

	var b strings.Builder
	for row := 0; row < menuMaxRows; row++ {
		if row > 0 {
			b.WriteByte('\n')
		}
		lineNotEmpty := false
		for index, column := range columns {
			if index > 0 {
				if columns[index-1].digit && !column.digit {
					b.WriteString("    ")
				} else {
					b.WriteString("  ")
				}
			}
			if row < len(column.hints) {
				b.WriteString(m.formatHint(column, row, skin))
				lineNotEmpty = true
			} else {
				b.WriteString(strings.Repeat(" ", column.width))
			}
		}
		if !lineNotEmpty {
			break
		}
	}
	m.SetText(strings.TrimRight(b.String(), "\n"))
}

func (m *Menu) columns() []menuColumn {
	digits := make([]MenuHint, 0, 10)
	hints := make([]MenuHint, 0, len(m.hints))
	for _, hint := range m.hints {
		if hint.Digit {
			digits = append(digits, hint)
		} else {
			hints = append(hints, hint)
		}
	}

	sort.SliceStable(digits, func(i, j int) bool {
		left, leftErr := strconv.Atoi(digits[i].Key)
		right, rightErr := strconv.Atoi(digits[j].Key)
		if leftErr == nil && rightErr == nil {
			return left < right
		}
		return digits[i].Key < digits[j].Key
	})

	columns := make([]menuColumn, 0)
	if len(digits) > 0 {
		columns = append(columns, chunkHints(digits, true)...)
	}
	columns = append(columns, chunkHints(hints, false)...)

	// Follow the requested column count formula, padding with empty columns
	// when the hint count leaves unused space.
	count := len(m.hints)/menuMaxRows + 1
	if len(digits) > 0 {
		count++
	}
	for len(columns) < count {
		columns = append(columns, menuColumn{})
	}

	for index := range columns {
		for _, hint := range columns[index].hints {
			if width := utf8.RuneCountInString(hint.Key) + 2; width > columns[index].keyWidth {
				columns[index].keyWidth = width
			}
		}
		width := columns[index].keyWidth
		for _, hint := range columns[index].hints {
			cell := width + 1 + utf8.RuneCountInString(hint.Description)
			if cell > columns[index].width {
				columns[index].width = cell
			}
		}
	}
	return columns
}

func chunkHints(hints []MenuHint, digit bool) []menuColumn {
	if len(hints) == 0 {
		return nil
	}
	columns := make([]menuColumn, 0, (len(hints)+menuMaxRows-1)/menuMaxRows)
	for len(hints) > 0 {
		end := menuMaxRows
		if end > len(hints) {
			end = len(hints)
		}
		columns = append(columns, menuColumn{
			hints: append([]MenuHint(nil), hints[:end]...),
			digit: digit,
		})
		hints = hints[end:]
	}
	return columns
}

func (m *Menu) formatHint(column menuColumn, row int, skin Skin) string {
	hint := column.hints[row]
	keyColor := skin.MenuKey
	if hint.Digit {
		keyColor = skin.MenuNum
	}
	if hint.Danger {
		keyColor = skin.Error
	}
	keyLabel := "<" + hint.Key + ">"
	padding := column.keyWidth - utf8.RuneCountInString(keyLabel)
	descriptionColor := skin.MenuFG
	if hint.Danger {
		descriptionColor = skin.Error
	}
	cell := fmt.Sprintf(
		"[#%06x::b]%s%s[-] %s%s[-]",
		uint32(keyColor.Hex()),
		keyLabel,
		strings.Repeat(" ", padding),
		colorTag(descriptionColor),
		tview.Escape(hint.Description),
	)
	plainWidth := column.keyWidth + 1 + utf8.RuneCountInString(hint.Description)
	if padding := column.width - plainWidth; padding > 0 {
		cell += strings.Repeat(" ", padding)
	}
	return cell
}
