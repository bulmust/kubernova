package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// StatusIndicator replaces the header when it is hidden.
type StatusIndicator struct {
	*tview.TextView

	skin    Skin
	content string
	state   ConnectionState
}

// NewStatusIndicator creates the one-row collapsed header indicator.
func NewStatusIndicator(manager *SkinManager) *StatusIndicator {
	indicator := &StatusIndicator{
		TextView: tview.NewTextView().
			SetDynamicColors(true).
			SetWrap(false),
		state: ConnectionConnected,
	}
	manager.Subscribe(indicator.ApplySkin)
	return indicator
}

// SetConnectionState updates the marker shown next to the content.
func (s *StatusIndicator) SetConnectionState(state ConnectionState) {
	s.state = state
	s.render()
}

// SetContent updates the collapsed header text.
func (s *StatusIndicator) SetContent(content string) {
	s.content = content
	s.render()
}

// ApplySkin applies a new skin and repaints.
func (s *StatusIndicator) ApplySkin(skin Skin) {
	s.skin = skin
	s.render()
}

func (s *StatusIndicator) render() {
	skin := s.skin
	s.SetBackgroundColor(skin.BodyBG)
	s.SetTextStyle(tcell.StyleDefault.
		Background(skin.BodyBG).
		Foreground(skin.InfoFG))
	content := strings.TrimSpace(s.content)
	if content == "" {
		content = "Kubernova"
	}
	s.SetText(fmt.Sprintf(
		" %s%s[-] %s%s[-]",
		colorTag(skin.Section),
		s.state.Marker(),
		colorTag(skin.InfoFG),
		tview.Escape(content),
	))
}
