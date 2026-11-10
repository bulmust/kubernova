package ui

import (
	"unicode/utf8"

	"github.com/rivo/tview"
)

// HeaderHeight is the fixed number of rows used by the full header.
const HeaderHeight = 7

// Header composes cluster info, menu, and emblem columns.
type Header struct {
	*tview.Flex

	clusterInfo *ClusterInfo
	menu        *Menu
	logo        *Logo

	clusterWidth int
	logoVisible  bool
	narrow       bool
}

// NewHeader creates the three-column header.
func NewHeader(manager *SkinManager) *Header {
	clusterInfo := NewClusterInfo(manager)
	menu := NewMenu(manager)
	logo := NewLogo(manager)

	header := &Header{
		Flex: tview.NewFlex().
			SetDirection(tview.FlexColumn).
			AddItem(clusterInfo, 50, 0, false).
			AddItem(menu, 0, 1, false).
			AddItem(logo, 26, 0, false),
		clusterInfo:  clusterInfo,
		menu:         menu,
		logo:         logo,
		clusterWidth: 50,
		logoVisible:  true,
	}
	header.SetBackgroundColor(manager.Current().BodyBG)
	manager.Subscribe(header.ApplySkin)
	return header
}

// ApplySkin repaints the header background when the skin changes.
func (h *Header) ApplySkin(skin Skin) {
	h.SetBackgroundColor(skin.BodyBG)
}

// SetClusterInfo updates the cluster key/value table.
func (h *Header) SetClusterInfo(data ClusterInfoData) {
	h.clusterInfo.SetData(data)
	name := data.ClusterName
	if name == "" {
		name = data.Cluster
	}
	width := 50
	if minimum := utf8.RuneCountInString(name) + 15; minimum > width {
		width = minimum
	}
	h.clusterWidth = width
	h.ResizeItem(h.clusterInfo, width, 0)
}

// SetHints replaces the shortcut hints.
func (h *Header) SetHints(hints []MenuHint) {
	h.menu.SetHints(hints)
}

// SetNarrow adjusts the header for the available terminal width. The emblem is
// hidden first, then the cluster column shrinks so menu columns can wrap.
func (h *Header) SetNarrow(width int) {
	if width <= 0 {
		return
	}
	h.narrow = width < 120
	h.logoVisible = width >= 110
	if h.logoVisible {
		h.ResizeItem(h.logo, 26, 0)
	} else {
		h.ResizeItem(h.logo, 0, 0)
	}

	clusterWidth := h.clusterWidth
	if width < 80 {
		clusterWidth = 24
	} else if width < 120 && clusterWidth > 34 {
		clusterWidth = 34
	}
	h.ResizeItem(h.clusterInfo, clusterWidth, 0)
}

// Logo returns the header logo so callers can post status messages.
func (h *Header) Logo() *Logo {
	return h.logo
}

// LogoVisible reports whether the responsive header currently shows the
// emblem.
func (h *Header) LogoVisible() bool {
	return h.logoVisible
}
