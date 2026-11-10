package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// LogoLevel is the severity of the logo status line.
type LogoLevel int

const (
	LogoNormal LogoLevel = iota
	LogoInfo
	LogoWarn
	LogoError
)

var logoArt = []string{
	"    _  __",
	"   | |/ /_ __",
	"   | ' /| '_ \\",
	"   | . \\| | | |",
	"   |_|\\_\\_| |_|",
	"    KUBERNOVA",
}

// Logo renders the six-row emblem and its one-row status line.
type Logo struct {
	*tview.TextView

	skin    Skin
	level   LogoLevel
	message string
}

// NewLogo creates the right header column and subscribes it to skin changes.
func NewLogo(manager *SkinManager) *Logo {
	logo := &Logo{
		TextView: tview.NewTextView().
			SetDynamicColors(true).
			SetWrap(false),
	}
	manager.Subscribe(logo.ApplySkin)
	return logo
}

// ApplySkin applies a new skin and repaints.
func (l *Logo) ApplySkin(skin Skin) {
	l.skin = skin
	l.render()
}

// Info shows an informational status.
func (l *Logo) Info(message string) {
	l.setStatus(LogoInfo, message)
}

// Warn shows a warning status.
func (l *Logo) Warn(message string) {
	l.setStatus(LogoWarn, message)
}

// Error shows an error status.
func (l *Logo) Error(message string) {
	l.setStatus(LogoError, message)
}

// Err is an alias for Error.
func (l *Logo) Err(message string) {
	l.Error(message)
}

// Reset restores the normal logo color and clears the status line.
func (l *Logo) Reset() {
	l.setStatus(LogoNormal, "")
}

func (l *Logo) setStatus(level LogoLevel, message string) {
	l.level = level
	l.message = message
	l.render()
}

func (l *Logo) render() {
	skin := l.skin
	l.SetBackgroundColor(skin.BodyBG)
	l.SetTextStyle(tcell.StyleDefault.
		Background(skin.BodyBG).
		Foreground(skin.BodyFG))
	l.SetTextAlign(tview.AlignCenter)

	artColor := skin.Logo
	statusBackground := skin.BodyBG
	switch l.level {
	case LogoInfo:
		artColor = skin.Info
		statusBackground = skin.Info
	case LogoWarn:
		artColor = skin.Warn
		statusBackground = skin.Warn
	case LogoError:
		artColor = skin.Error
		statusBackground = skin.Error
	}

	var b strings.Builder
	for _, line := range logoArt {
		fmt.Fprintf(&b, "%s%s[-]\n", colorTag(artColor), tview.Escape(line))
	}
	message := l.message
	if message == "" {
		b.WriteString(strings.Repeat(" ", 26))
	} else {
		if width := utf8.RuneCountInString(message); width < 26 {
			message += strings.Repeat(" ", 26-width)
		}
		fmt.Fprintf(
			&b,
			"%s%s[-]",
			colorPairTag(skin.BodyBG, statusBackground),
			tview.Escape(message),
		)
	}
	l.SetText(b.String())
}
