package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func newTestHeader() (*Header, *SkinManager) {
	manager := NewSkinManager(DefaultSkin())
	return NewHeader(manager), manager
}

func lineCount(text string) int {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func TestClusterInfoShowsClusterAndPrometheus(t *testing.T) {
	header, _ := newTestHeader()
	header.SetClusterInfo(ClusterInfoData{
		ClusterName: "prod-cluster",
		Cluster:     "prod-cluster ●",
		Prometheus:  "http://prometheus:9090 ●",
	})

	text := header.clusterInfo.GetText(true)
	if got := lineCount(text); got != 2 {
		t.Errorf("cluster info rows = %d, want 2", got)
	}
	for _, label := range []string{"Cluster:", "Prometheus:"} {
		if !strings.Contains(text, label) {
			t.Errorf("cluster info %q does not contain %q", text, label)
		}
	}
	if !strings.Contains(text, "prod-cluster") || !strings.Contains(text, "prometheus:9090") {
		t.Errorf("cluster info %q is missing values", text)
	}
}

func TestMenuPutsDigitHintsFirst(t *testing.T) {
	header, _ := newTestHeader()
	header.SetHints([]MenuHint{
		{Key: "?", Description: "Help"},
		{Key: "1", Description: "argocd", Digit: true},
		{Key: "0", Description: "all", Digit: true},
		{Key: "ctrl+p", Description: "options"},
	})

	text := header.menu.GetText(true)
	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		t.Fatalf("menu %q is missing rows", text)
	}
	if !strings.HasPrefix(lines[0], "<0>") {
		t.Errorf("first menu cell = %q, want a digit hint", lines[0])
	}
	if !strings.HasPrefix(lines[1], "<1>") {
		t.Errorf("second menu row = %q, want the next digit hint", lines[1])
	}
	if !strings.Contains(text, "<?>") || !strings.Contains(text, "<ctrl+p>") {
		t.Fatalf("menu %q is missing non-digit hints", text)
	}
	firstLine := lines[0]
	digitEnd := strings.Index(firstLine, "all") + len("all")
	shortcutStart := strings.Index(firstLine, "<?>")
	if shortcutStart-digitEnd < 4 {
		t.Errorf("namespace and shortcut columns are not separated by four spaces:\n%s", firstLine)
	}
	if got := lineCount(text); got > menuMaxRows {
		t.Errorf("menu row count = %d, want at most %d", got, menuMaxRows)
	}
}

func TestMenuPadsKeysWithinAColumn(t *testing.T) {
	header, _ := newTestHeader()
	header.SetHints([]MenuHint{
		{Key: "a", Description: "First"},
		{Key: "Ctrl+X", Description: "Second"},
	})

	text := header.menu.GetText(true)
	if !strings.Contains(text, "<a>      First") {
		t.Errorf("menu keys are not aligned:\n%s", text)
	}
}

func TestLogoHasSixArtRowsAndOneStatusRow(t *testing.T) {
	header, _ := newTestHeader()
	text := header.logo.GetText(true)
	if got := lineCount(text); got != 7 {
		t.Errorf("logo rows = %d, want 7", got)
	}
	for _, line := range logoArt {
		if len([]rune(line)) > 26 {
			t.Errorf("logo art line %q is wider than 26 characters", line)
		}
	}
}

func TestLogoStatusAndReset(t *testing.T) {
	header, _ := newTestHeader()
	header.Logo().Warn("high cpu")
	if text := header.logo.GetText(true); !strings.Contains(text, "high cpu") {
		t.Errorf("logo status %q does not contain the warning", text)
	}
	header.Logo().Reset()
	if text := header.logo.GetText(true); strings.Contains(text, "high cpu") {
		t.Errorf("logo status was not cleared:\n%s", text)
	}
}

func TestSkinChangesRepaintHeaderObservers(t *testing.T) {
	header, manager := newTestHeader()
	next := DefaultSkin()
	next.BodyBG = tcell.NewHexColor(0x112233)
	next.Section = tcell.NewHexColor(0x445566)
	manager.SetSkin(next)

	if got := header.clusterInfo.GetBackgroundColor(); got != next.BodyBG {
		t.Errorf("cluster info background = %v, want %v", got, next.BodyBG)
	}
	if got := header.menu.GetBackgroundColor(); got != next.BodyBG {
		t.Errorf("menu background = %v, want %v", got, next.BodyBG)
	}
	if got := header.logo.GetBackgroundColor(); got != next.BodyBG {
		t.Errorf("logo background = %v, want %v", got, next.BodyBG)
	}
}

func TestStatusIndicatorUsesSkin(t *testing.T) {
	manager := NewSkinManager(DefaultSkin())
	indicator := NewStatusIndicator(manager)
	indicator.SetContent("Kubernova")

	if got := lineCount(indicator.GetText(true)); got != 1 {
		t.Errorf("status indicator rows = %d, want 1", got)
	}
	next := DefaultSkin()
	next.BodyBG = tcell.NewHexColor(0x101010)
	manager.SetSkin(next)
	if got := indicator.GetBackgroundColor(); got != next.BodyBG {
		t.Errorf("status indicator background = %v, want %v", got, next.BodyBG)
	}
}

func TestHeaderColumnOrderAndResponsiveEmblem(t *testing.T) {
	header, _ := newTestHeader()
	if got := header.GetItemCount(); got != 3 {
		t.Fatalf("header item count = %d, want cluster, menu, emblem", got)
	}
	header.SetNarrow(80)
	if header.LogoVisible() {
		t.Error("emblem should be hidden on a narrow terminal")
	}
	header.SetNarrow(160)
	if !header.LogoVisible() {
		t.Error("emblem should be shown on a wide terminal")
	}
}

func TestMenuDangerHintUsesErrorColor(t *testing.T) {
	header, manager := newTestHeader()
	header.SetHints([]MenuHint{{Key: "ctrl+r", Description: "refresh", Danger: true}})
	text := header.menu.GetText(false)
	want := fmt.Sprintf("#%06x", uint32(manager.Current().Error.Hex()))
	if !strings.Contains(text, want) {
		t.Errorf("danger hint %q does not use error color %q", text, want)
	}
}

func TestConnectionStateMarkersAreTextual(t *testing.T) {
	tests := map[ConnectionState]string{
		ConnectionConnected:     "●",
		ConnectionNotConfigured: "○",
		ConnectionDisconnected:  "○",
		ConnectionConnecting:    "○",
		ConnectionError:         "✗",
	}
	for state, want := range tests {
		if got := state.Marker(); got != want {
			t.Errorf("ConnectionState(%d).Marker() = %q, want %q", state, got, want)
		}
	}
}

func TestMenuKeepsHintOrderSoRefreshComesFirst(t *testing.T) {
	header, _ := newTestHeader()
	header.SetHints([]MenuHint{
		{Key: "ctrl+r", Description: "refresh"},
		{Key: "/", Description: "filter text"},
	})
	text := header.menu.GetText(true)
	firstLine := strings.Split(text, "\n")[0]
	if !strings.HasPrefix(firstLine, "<ctrl+r>") {
		t.Errorf("first shortcut = %q, want ctrl+r", firstLine)
	}
}
