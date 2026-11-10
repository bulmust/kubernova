package ui

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
)

// Skin is the resolved color palette used by header components.
type Skin struct {
	BodyFG   tcell.Color
	BodyBG   tcell.Color
	Logo     tcell.Color
	InfoFG   tcell.Color
	Section  tcell.Color
	MenuFG   tcell.Color
	MenuKey  tcell.Color
	MenuNum  tcell.Color
	StatusOK tcell.Color
	Error    tcell.Color
	Warn     tcell.Color
	Info     tcell.Color
}

// DefaultSkin is used only when a configured color cannot be parsed.
func DefaultSkin() Skin {
	return Skin{
		BodyFG:   tcell.NewHexColor(0xffffff),
		BodyBG:   tcell.NewHexColor(0x000000),
		Logo:     tcell.NewHexColor(0xff00ff),
		InfoFG:   tcell.NewHexColor(0xffffff),
		Section:  tcell.NewHexColor(0xffa500),
		MenuFG:   tcell.NewHexColor(0xa8a8a8),
		MenuKey:  tcell.NewHexColor(0x5fa8ff),
		MenuNum:  tcell.NewHexColor(0xb266ff),
		StatusOK: tcell.NewHexColor(0xffffff),
		Error:    tcell.NewHexColor(0xff5555),
		Warn:     tcell.NewHexColor(0xffb86c),
		Info:     tcell.NewHexColor(0x61afef),
	}
}

// ParseColor converts a #RRGGBB value or tcell color name into a color.
func ParseColor(value string, fallback tcell.Color) tcell.Color {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "#") && len(value) == 7 {
		number, err := strconv.ParseInt(value[1:], 16, 32)
		if err == nil {
			return tcell.NewHexColor(int32(number))
		}
	}
	if value != "" {
		if color := tcell.GetColor(value); color != tcell.ColorDefault {
			return color
		}
	}
	return fallback
}

func colorTag(color tcell.Color) string {
	return fmt.Sprintf("[#%06x]", uint32(color.Hex()))
}

func colorPairTag(foreground, background tcell.Color) string {
	return fmt.Sprintf("[#%06x:#%06x]", uint32(foreground.Hex()), uint32(background.Hex()))
}

// SkinManager stores the active skin and notifies observers when it changes.
type SkinManager struct {
	mu        sync.RWMutex
	skin      Skin
	listeners []func(Skin)
}

// NewSkinManager creates a skin manager.
func NewSkinManager(skin Skin) *SkinManager {
	return &SkinManager{skin: skin}
}

// Current returns the active skin.
func (m *SkinManager) Current() Skin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.skin
}

// Subscribe registers a listener and immediately sends it the current skin.
func (m *SkinManager) Subscribe(listener func(Skin)) {
	m.mu.Lock()
	m.listeners = append(m.listeners, listener)
	skin := m.skin
	m.mu.Unlock()
	listener(skin)
}

// SetSkin updates the skin and repaints all registered observers.
func (m *SkinManager) SetSkin(skin Skin) {
	m.mu.Lock()
	m.skin = skin
	listeners := append([]func(Skin){}, m.listeners...)
	m.mu.Unlock()
	for _, listener := range listeners {
		listener(skin)
	}
}
