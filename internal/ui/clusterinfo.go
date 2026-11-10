package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ClusterInfoData is the key/value data shown in the left header column.
type ClusterInfoData struct {
	ClusterName string
	Context     string
	Cluster     string
	Prometheus  string
	Status      string
}

// ClusterInfo renders the cluster and Prometheus status table.
type ClusterInfo struct {
	*tview.TextView

	data ClusterInfoData
	skin Skin
}

// NewClusterInfo creates the left header column and subscribes it to skin
// changes.
func NewClusterInfo(manager *SkinManager) *ClusterInfo {
	info := &ClusterInfo{
		TextView: tview.NewTextView().
			SetDynamicColors(true).
			SetWrap(false),
	}
	manager.Subscribe(info.ApplySkin)
	return info
}

// SetData updates the cluster information and repaints.
func (c *ClusterInfo) SetData(data ClusterInfoData) {
	c.data = data
	c.render()
}

// ApplySkin applies a new skin and repaints.
func (c *ClusterInfo) ApplySkin(skin Skin) {
	c.skin = skin
	c.render()
}

func (c *ClusterInfo) render() {
	skin := c.skin
	c.SetBackgroundColor(skin.BodyBG)
	c.SetTextStyle(tcell.StyleDefault.
		Background(skin.BodyBG).
		Foreground(skin.InfoFG))

	rows := []struct {
		label string
		value string
	}{
		{"Cluster", valueOrDash(c.data.Cluster)},
	}
	if strings.TrimSpace(c.data.Context) != "" {
		rows = append(rows, struct {
			label string
			value string
		}{"Context", c.data.Context})
	}
	rows = append(rows, struct {
		label string
		value string
	}{"Prometheus", valueOrDash(c.data.Prometheus)})
	if strings.TrimSpace(c.data.Status) != "" {
		rows = append(rows, struct {
			label string
			value string
		}{"Status", c.data.Status})
	}

	var b strings.Builder
	for index, row := range rows {
		if index > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b,
			" %s%-11s[-] %s%s[-]",
			colorTag(skin.Section),
			row.label+":",
			colorTag(skin.InfoFG),
			tview.Escape(row.value),
		)
	}
	c.SetText(b.String())
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
