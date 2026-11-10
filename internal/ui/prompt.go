package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// PromptInput is an input field that can render an inline completion suffix as
// gray text instead of opening tview's autocomplete dropdown.
type PromptInput struct {
	*tview.InputField

	suggestion string
}

// NewPromptInput creates an inline-completion input field.
func NewPromptInput() *PromptInput {
	return &PromptInput{InputField: tview.NewInputField()}
}

// SetSuggestion sets the completion suffix shown after the current text.
func (p *PromptInput) SetSuggestion(suggestion string) {
	p.suggestion = suggestion
}

// Suggestion returns the completion suffix currently shown.
func (p *PromptInput) Suggestion() string {
	return p.suggestion
}

// Draw renders the input field and then overlays the suggestion suffix in gray.
func (p *PromptInput) Draw(screen tcell.Screen) {
	p.InputField.Draw(screen)
	if p.suggestion == "" || !p.HasFocus() {
		return
	}
	x, y, width, _ := p.GetInnerRect()
	if width <= 0 {
		return
	}
	labelWidth := tview.TaggedStringWidth(p.GetLabel())
	textWidth := tview.TaggedStringWidth(p.GetText())
	column := x + labelWidth + textWidth
	style := p.GetFieldStyle().Foreground(tcell.ColorGray)
	for _, r := range p.suggestion {
		if column >= x+width {
			break
		}
		screen.SetContent(column, y, r, nil, style)
		column++
	}
}
