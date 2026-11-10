package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"kubernova/internal/model"
)

func initialVisibleColumns() int {
	count := 0
	for _, column := range model.Columns {
		if !column.HiddenByDefault {
			count++
		}
	}
	return count
}

// cellID returns the text of the cell in the named column, wherever that
// column currently sits among the visible columns.
func cellID(t *testing.T, table *ContainerTable, row int, id string) string {
	t.Helper()
	return table.GetCell(row, columnPosition(t, table, id)).Text
}

// columnPosition returns the displayed position of a visible column.
func columnPosition(t *testing.T, table *ContainerTable, id string) int {
	t.Helper()
	target := ColumnIndex(id)
	for position, column := range table.visibleColumns() {
		if column == target {
			return position
		}
	}
	t.Fatalf("column %s is not visible", id)
	return -1
}

// fullTable shows every column except the UID and Prometheus ones.
func fullTable(containers []model.Container) *ContainerTable {
	table := NewContainerTable(containers)
	visibility := table.ColumnVisibility()
	for index, column := range model.Columns {
		visibility[index] = !strings.HasPrefix(column.ID, "P") && column.ID != "K14" && column.ID != "K15"
	}
	table.SetColumnVisibility(visibility)
	return table
}

func testContainers() []model.Container {
	return []model.Container{
		{Cluster: "minikube", Namespace: "default", OwnerName: "nginx", Owners: "ReplicaSet", Pod: "nginx-abc", Image: "nginx:1.27", ResourceSource: "LimitRange", Name: "nginx", CPUReq: "100m", CPULim: "500m", MemReq: "128Mi", MemLim: "256Mi", OOM: "no"},
		{Cluster: "minikube", Namespace: "default", OwnerName: "redis", Owners: "ReplicaSet", Pod: "redis-xyz", Image: "redis:7.2", ResourceSource: "Pod", Name: "redis", CPUReq: "200m", CPULim: "500m", MemReq: "256Mi", MemLim: "512Mi", OOM: "yes"},
		{Cluster: "minikube", Namespace: "monitoring", OwnerName: "prometheus", Owners: "StatefulSet", Pod: "prometheus-0", Image: "prom/prometheus:v2.53.0", ResourceSource: "Pod", Name: "prometheus", CPUReq: "500m", CPULim: "1", MemReq: "2Gi", MemLim: "4Gi", OOM: "no"},
		{Cluster: "minikube", Namespace: "monitoring", OwnerName: "grafana", Owners: "StatefulSet", Pod: "grafana-0", Image: "grafana/grafana:11.1.0", ResourceSource: "Pod", Name: "grafana", CPUReq: "100m", CPULim: "300m", MemReq: "256Mi", MemLim: "512Mi", OOM: "yes"},
		{Cluster: "minikube", Namespace: "kube-system", OwnerName: "coredns", Owners: "DaemonSet", Pod: "coredns-abc", Image: "coredns/coredns:1.11.1", ResourceSource: "Pod", Name: "coredns", CPUReq: "100m", CPULim: "200m", MemReq: "70Mi", MemLim: "170Mi", OOM: "no"},
		{Cluster: "minikube", Namespace: "backend", OwnerName: "", Owners: "none", Pod: "api-0", Image: "api:latest", ResourceSource: "Pod", Name: "api", CPUReq: "250m", CPULim: "1", MemReq: "512Mi", MemLim: "1Gi", OOM: "no"},
	}
}

func testReplicaContainers() []model.Container {
	return []model.Container{
		{Cluster: "minikube", Namespace: "default", OwnerName: "web", Owners: "StatefulSet", Pod: "web-0", Image: "app:1", ResourceSource: "Pod", Name: "app", OOM: "no"},
		{Cluster: "minikube", Namespace: "default", OwnerName: "web", Owners: "StatefulSet", Pod: "web-0", Image: "sidecar:1", ResourceSource: "Pod", Name: "sidecar", OOM: "no"},
		{Cluster: "minikube", Namespace: "default", OwnerName: "web", Owners: "StatefulSet", Pod: "web-1", Image: "app:1", ResourceSource: "Pod", Name: "app", OOM: "no"},
		{Cluster: "minikube", Namespace: "default", OwnerName: "web", Owners: "StatefulSet", Pod: "web-2", Image: "app:1", ResourceSource: "Pod", Name: "app", OOM: "no"},
		{Cluster: "minikube", Namespace: "kube-system", OwnerName: "agent", Owners: "DaemonSet", Pod: "agent-x", Image: "agent:1", ResourceSource: "Pod", Name: "agent", OOM: "no"},
	}
}

func TestSetFilter(t *testing.T) {
	containers := testContainers()
	tests := []struct {
		name     string
		query    string
		wantRows int // Including the header row.
	}{
		{"empty filter shows everything", "", 1 + len(containers)},
		{"matches cluster", "minikube", 1 + len(containers)},
		{"matches namespace", "monitoring", 3},
		{"matches owner", "DaemonSet", 2},
		{"matches owner name", "coredns", 2},
		{"matches resource source", "LimitRange", 2},
		{"matches container", "coredns", 2},
		{"matches image", "prom/prometheus", 2},
		{"matches oom", "yes", 3},
		{"case insensitive", "REDIS", 2},
		{"matches memory column", "256Mi", 4},
		{"matches CPU column", "500m", 4},
		{"no matches leaves header", "does-not-exist", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table := fullTable(containers)
			table.SetFilter(tt.query)

			if got := table.GetRowCount(); got != tt.wantRows {
				t.Errorf("row count after filtering %q = %d, want %d", tt.query, got, tt.wantRows)
			}
		})
	}
}

func TestSetFilterHighlightsMatches(t *testing.T) {
	table := NewContainerTable(testContainers())
	table.SetFilter("REDIS")

	if got, want := cellID(t, table, 1, "K08"), "[red]redis[-]"; got != want {
		t.Errorf("highlighted cell text = %q, want %q", got, want)
	}
}

func TestHighlightEscapesText(t *testing.T) {
	table := NewContainerTable([]model.Container{
		{Namespace: "default", Pod: "web", Name: "[red]nginx"},
	})
	table.SetFilter("nginx")

	if got, want := cellID(t, table, 1, "K08"), "[red[][red]nginx[-]"; got != want {
		t.Errorf("escaped cell text = %q, want %q", got, want)
	}
}

func TestSetFilterKeepsSelectedContainer(t *testing.T) {
	table := NewContainerTable(testContainers())

	// Select monitoring/grafana-0 (row 4: three containers precede it).
	table.Select(4, 0)
	table.SetFilter("monitoring")

	row, _ := table.GetSelection()
	if got := cellID(t, table, row, "K08"); got != "grafana" {
		t.Fatalf("selected container after filtering = %q, want %q", got, "grafana")
	}

	table.SetFilter("")
	row, _ = table.GetSelection()
	if got := cellID(t, table, row, "K08"); got != "grafana" {
		t.Fatalf("selected container after clearing filter = %q, want %q", got, "grafana")
	}
}

func TestSetFilterUpdatesTitle(t *testing.T) {
	table := NewContainerTable(testContainers())
	table.SetFilter("redis")

	if title := table.GetTitle(); !strings.Contains(title, "redis") {
		t.Errorf("title %q does not contain the filter query", title)
	}
}

func TestSetNamespaceUpdatesTitle(t *testing.T) {
	table := NewContainerTable(nil)
	table.SetNamespace("monitoring")

	if title := table.GetTitle(); !strings.Contains(title, "Kubernova(monitoring)") {
		t.Errorf("title %q does not contain the namespace", title)
	}
}

func TestSetNamespaceFiltersRows(t *testing.T) {
	table := NewContainerTable(testContainers())

	table.SetNamespace("monitoring")
	if got := table.GetRowCount(); got != 3 {
		t.Errorf("row count for monitoring = %d, want 3", got)
	}
	if got := cellID(t, table, 1, "K02"); got != "monitoring" {
		t.Errorf("first namespace row = %q, want monitoring", got)
	}
	if title := table.GetTitle(); !strings.Contains(title, "Kubernova(monitoring)[2]") {
		t.Errorf("title %q does not show the filtered row count", title)
	}

	table.SetNamespace("-A")
	if got := table.GetRowCount(); got != 1+len(testContainers()) {
		t.Errorf("row count for all = %d, want %d", got, 1+len(testContainers()))
	}
	if title := table.GetTitle(); !strings.Contains(title, "Kubernova(all)") {
		t.Errorf("title %q does not show all", title)
	}
}

func TestSetContainers(t *testing.T) {
	table := NewContainerTable(nil)
	containers := testContainers()

	table.SetContainers(containers)

	if got := table.GetRowCount(); got != 1+len(containers) {
		t.Errorf("row count = %d, want %d", got, 1+len(containers))
	}
	if got := cellID(t, table, 1, "K08"); got != "nginx" {
		t.Errorf("first container = %q, want nginx", got)
	}
}

func TestSetFilterFiltersByCluster(t *testing.T) {
	containers := testContainers()
	for index := range containers {
		containers[index].Cluster = "dev-cluster"
	}

	table := fullTable(containers)
	table.SetFilter("dev-cluster")

	if got := table.GetRowCount(); got != 1+len(containers) {
		t.Errorf("row count filtering by cluster = %d, want %d", got, 1+len(containers))
	}
}

func TestColumnIDsAndHiddenColumns(t *testing.T) {
	table := NewContainerTable(testContainers())

	wantOrder := []string{"K02", "K04", "K03", "K05", "K08", "K19", "K09", "K10", "K11", "K12", "K13", "K17", "K16"}
	if got := table.GetColumnCount(); got != len(wantOrder) {
		t.Fatalf("default column count = %d, want %d", got, len(wantOrder))
	}
	for position, id := range wantOrder {
		header := table.GetCell(0, position).Text
		if !strings.Contains(header, id) || !strings.Contains(header, model.Columns[ColumnIndex(id)].Header) {
			t.Errorf("header %d = %q, want %s", position, header, id)
		}
	}
	for _, id := range []string{"K01", "K06", "K07", "K18"} {
		if table.ColumnVisible(ColumnIndex(id)) {
			t.Errorf("%s should be hidden by default", id)
		}
	}

	namespace := ColumnIndex("K02")
	table.ToggleColumn(namespace)
	if table.ColumnVisible(namespace) {
		t.Fatal("namespace column is still visible after toggling")
	}
	if got, want := table.GetColumnCount(), len(wantOrder)-1; got != want {
		t.Errorf("column count = %d, want %d", got, want)
	}
	if got := table.GetCell(0, 0).Text; !strings.Contains(got, "OWNER KIND") || !strings.Contains(got, "K04") {
		t.Errorf("header after hiding namespace = %q, want owner kind", got)
	}
	if got := cellID(t, table, 1, "K03"); got != "nginx" {
		t.Errorf("owner name after hiding namespace = %q, want nginx", got)
	}

	table.ToggleColumn(namespace)
	if !table.ColumnVisible(namespace) {
		t.Fatal("namespace column is still hidden after toggling back")
	}
	if got := table.GetCell(1, 0).Text; got != "default" {
		t.Errorf("first data cell after showing namespace = %q, want default", got)
	}
}

func TestToggleClusterColumnAddsAndRemovesItCompletely(t *testing.T) {
	table := NewContainerTable(testContainers())
	cluster := ColumnIndex("K01")

	table.ToggleColumn(cluster)
	if got := cellID(t, table, 1, "K01"); got != "minikube" {
		t.Fatalf("cluster cell = %q, want minikube", got)
	}
	if got := table.GetCell(1, 0).Text; got != "default" {
		t.Errorf("first data cell with cluster shown = %q, want default", got)
	}

	table.ToggleColumn(cluster)
	if got, want := table.GetColumnCount(), initialVisibleColumns(); got != want {
		t.Errorf("column count after hiding cluster = %d, want %d", got, want)
	}
	for position := 0; position < table.GetColumnCount(); position++ {
		if strings.Contains(table.GetCell(0, position).Text, "CLUSTER") {
			t.Errorf("CLUSTER header still present at position %d", position)
		}
	}
}

func TestToggleOwnerKindColumnRemovesItCompletely(t *testing.T) {
	table := NewContainerTable(testContainers())
	kind := ColumnIndex("K04")

	if got := cellID(t, table, 1, "K04"); got != "ReplicaSet" {
		t.Fatalf("owner kind cell = %q, want ReplicaSet", got)
	}
	table.ToggleColumn(kind)
	if got := table.GetCell(0, 1).Text; !strings.Contains(got, "OWNER NAME") || !strings.Contains(got, "K03") {
		t.Errorf("header after hiding owner kind = %q, want owner name", got)
	}
	if got := table.GetCell(1, 1).Text; got != "nginx" {
		t.Errorf("owner name cell after hiding owner kind = %q, want nginx", got)
	}

	table.ToggleColumn(kind)
	if got := cellID(t, table, 1, "K04"); got != "ReplicaSet" {
		t.Errorf("owner kind cell after showing = %q, want ReplicaSet", got)
	}
}

func TestToggleSelectedHidden(t *testing.T) {
	table := NewContainerTable(testContainers())
	table.Select(1, 0)

	if !table.ToggleSelectedHidden() {
		t.Fatal("ToggleSelectedHidden() = false, want true")
	}

	row, _ := table.GetSelection()
	if row != 1 {
		t.Errorf("selection row = %d, want 1", row)
	}
	if got := cellID(t, table, row, "K08"); got != "redis" {
		t.Errorf("row after hidden item = %q, want redis", got)
	}

	hiddenRow := len(testContainers())
	if got := cellID(t, table, hiddenRow, "K08"); got != "nginx" {
		t.Errorf("hidden row container = %q, want nginx", got)
	}
	if fg, _, _ := table.GetCell(hiddenRow, 0).Style.Decompose(); fg != tcell.ColorGray {
		t.Errorf("hidden row color = %v, want gray", fg)
	}

	table.Select(hiddenRow, 0)
	table.ToggleSelectedHidden()
	row, _ = table.GetSelection()
	if row != hiddenRow {
		t.Errorf("selection after restore = %d, want %d", row, hiddenRow)
	}
	if got := cellID(t, table, 1, "K08"); got != "nginx" {
		t.Errorf("restored row container = %q, want nginx", got)
	}
}

func TestToggleReplicaCollapse(t *testing.T) {
	table := NewContainerTable(testReplicaContainers())

	if !table.ReplicaCollapsed() {
		t.Fatal("ReplicaCollapsed() = false, want true by default")
	}
	if got := table.GetRowCount(); got != 1+3 {
		t.Errorf("collapsed row count = %d, want %d", got, 1+3)
	}

	table.ToggleReplicaCollapse()
	if table.ReplicaCollapsed() {
		t.Fatal("ReplicaCollapsed() = true, want false")
	}
	if got := table.GetRowCount(); got != 1+len(testReplicaContainers()) {
		t.Errorf("expanded row count = %d, want %d", got, 1+len(testReplicaContainers()))
	}

	table.ToggleReplicaCollapse()
	if got := table.GetRowCount(); got != 1+3 {
		t.Errorf("collapsed row count = %d, want %d", got, 1+3)
	}
}

func TestReplicaCollapseKeepsHiddenRowsAtEnd(t *testing.T) {
	table := NewContainerTable(testReplicaContainers())
	table.ToggleReplicaCollapse() // Expand before hiding a replica.
	table.Select(3, 0)            // web-1/app
	table.ToggleSelectedHidden()

	table.ToggleReplicaCollapse()

	if got := table.GetRowCount(); got != 1+4 {
		t.Errorf("row count = %d, want %d", got, 1+4)
	}

	hiddenRow := table.GetRowCount() - 1
	if got := cellID(t, table, hiddenRow, "K05"); got != "web-1" {
		t.Errorf("hidden pod = %q, want web-1", got)
	}
	if got := cellID(t, table, hiddenRow, "K08"); got != "app" {
		t.Errorf("hidden container = %q, want app", got)
	}
	if fg, _, _ := table.GetCell(hiddenRow, 0).Style.Decompose(); fg != tcell.ColorGray {
		t.Errorf("hidden row color = %v, want gray", fg)
	}

	table.ToggleReplicaCollapse()
	if got := table.GetRowCount(); got != 1+len(testReplicaContainers()) {
		t.Errorf("expanded row count = %d, want %d", got, 1+len(testReplicaContainers()))
	}
}

func TestFilterUsesVisibleColumns(t *testing.T) {
	table := NewContainerTable(testContainers())
	namespace := ColumnIndex("K02")
	table.ToggleColumn(namespace) // Hide namespace.

	table.SetFilter("monitoring")
	if got := table.GetRowCount(); got != 1 {
		t.Errorf("row count with namespace hidden = %d, want header only", got)
	}

	table.ToggleColumn(namespace) // Show namespace again.
	if got := table.GetRowCount(); got != 3 {
		t.Errorf("row count with namespace visible = %d, want 3", got)
	}

	// The cluster column is hidden by default, so it is not searched.
	table.SetFilter("minikube")
	if got := table.GetRowCount(); got != 1 {
		t.Errorf("row count with cluster hidden = %d, want header only", got)
	}
	table.ToggleColumn(ColumnIndex("K01"))
	if got := table.GetRowCount(); got != 1+len(testContainers()) {
		t.Errorf("row count with cluster shown = %d, want all rows", got)
	}
}

func TestHiddenUIDColumnsAreSelectableAndKeepStableIDs(t *testing.T) {
	table := NewContainerTable([]model.Container{{ContainerUID: "pod-uid", OwnerUID: "owner-uid"}})
	containerUID, ownerUID := ColumnIndex("K14"), ColumnIndex("K15")

	if table.ColumnVisible(containerUID) || table.ColumnVisible(ownerUID) {
		t.Fatal("UID columns should be hidden by default")
	}
	if got := ColumnID(containerUID); got != "K14" {
		t.Errorf("ColumnID(%d) = %q, want K14", containerUID, got)
	}

	table.SetColumnVisible(containerUID, true)
	table.SetColumnVisible(ownerUID, true)
	position := columnPosition(t, table, "K14")
	if got := table.GetCell(0, position).Text; !strings.Contains(got, "CONTAINER UID") || !strings.Contains(got, "K14") {
		t.Errorf("container UID header = %q, want CONTAINER UID and K14", got)
	}
	if got := cellID(t, table, 1, "K14"); got != "pod-uid" {
		t.Errorf("container UID cell = %q, want pod-uid", got)
	}
	if got := cellID(t, table, 1, "K15"); got != "owner-uid" {
		t.Errorf("owner UID cell = %q, want owner-uid", got)
	}

	table.ToggleColumn(ColumnIndex("K02"))
	if got := table.GetCell(0, 0).Text; !strings.Contains(got, "K04") {
		t.Errorf("stable ID after hiding K02 = %q, want K04", got)
	}
}

func TestSortingUsesStableColumnIDs(t *testing.T) {
	table := NewContainerTable(testContainers())
	table.SetSort("K02", false) // NAMESPACE ascending
	if got := cellID(t, table, 1, "K02"); got != "backend" {
		t.Errorf("first namespace after ascending sort = %q, want backend", got)
	}
	headerCell := table.GetCell(0, columnPosition(t, table, "K02"))
	if !strings.Contains(headerCell.Text, "▲") {
		t.Errorf("ascending sort marker missing from %q", headerCell.Text)
	}
	fg, _, attrs := headerCell.Style.Decompose()
	if fg != tcell.ColorLightPink || attrs&tcell.AttrBold == 0 {
		t.Errorf("sorted header style = fg %v attrs %v, want light pink and bold", fg, attrs)
	}
	table.SetSortDescending(true)
	if got := cellID(t, table, 1, "K02"); got != "monitoring" {
		t.Errorf("first namespace after descending sort = %q, want monitoring", got)
	}
	if text := table.GetCell(0, columnPosition(t, table, "K02")).Text; !strings.Contains(text, "▼") {
		t.Errorf("descending sort marker missing from %q", text)
	}
	if got := table.SortColumnID(); got != "K02" {
		t.Errorf("SortColumnID() = %q, want K02", got)
	}
}

func TestSortingBreaksTiesByNamespaceThenPod(t *testing.T) {
	containers := []model.Container{
		{Namespace: "b", Pod: "pod-2", Name: "x", Owners: "Deployment"},
		{Namespace: "a", Pod: "pod-10", Name: "x", Owners: "Deployment"},
		{Namespace: "b", Pod: "pod-1", Name: "x", Owners: "Deployment"},
		{Namespace: "a", Pod: "pod-2", Name: "x", Owners: "Deployment"},
	}
	want := []string{"a/pod-2", "a/pod-10", "b/pod-1", "b/pod-2"}
	table := NewContainerTable(containers)
	table.SetSort("K04", true) // every owner kind ties; descending must not flip the tie-break
	for index, expected := range want {
		got := cellID(t, table, index+1, "K02") + "/" + cellID(t, table, index+1, "K05")
		if got != expected {
			t.Errorf("row %d = %q, want %q", index, got, expected)
		}
	}
	sorted := SortContainers(containers, "K04", true)
	for index, expected := range want {
		if got := sorted[index].Namespace + "/" + sorted[index].Pod; got != expected {
			t.Errorf("SortContainers row %d = %q, want %q", index, got, expected)
		}
	}
}

func TestSortingByHiddenColumnStillWorks(t *testing.T) {
	containers := []model.Container{
		{Cluster: "c", Namespace: "n", Pod: "p1", Name: "a", Image: "z:1"},
		{Cluster: "c", Namespace: "n", Pod: "p2", Name: "b", Image: "a:1"},
	}
	table := NewContainerTable(containers)
	if table.ColumnVisible(ColumnIndex("K06")) {
		t.Fatal("image column should be hidden by default")
	}
	table.SetSort("K06", false)
	if got := cellID(t, table, 1, "K05"); got != "p2" {
		t.Errorf("first pod when sorted by hidden image = %q, want p2", got)
	}
	if got := table.SortColumnID(); got != "K06" {
		t.Errorf("SortColumnID() = %q, want K06", got)
	}
	if got := table.MoveSortColumn(1); got == "K06" {
		t.Error("MoveSortColumn selected a hidden column")
	}
}

func TestMoveSortColumnSkipsHiddenColumns(t *testing.T) {
	table := NewContainerTable(testContainers())
	table.ToggleColumn(ColumnIndex("K02"))
	if got := table.MoveSortColumn(1); got != "K04" {
		t.Errorf("MoveSortColumn(1) = %q, want K04", got)
	}
	if got := table.MoveSortColumn(-1); got != "K16" {
		t.Errorf("MoveSortColumn(-1) from first = %q, want K16 wrap-around", got)
	}
}

func TestSortedHiddenRowsStayAtEnd(t *testing.T) {
	table := NewContainerTable(testContainers())
	table.Select(1, 0)
	table.ToggleSelectedHidden()
	table.SetSort("K03", false) // OWNER NAME ascending

	last := table.GetRowCount() - 1
	if got := cellID(t, table, last, "K03"); got != "nginx" {
		t.Errorf("hidden row owner = %q, want nginx", got)
	}
}

func TestCacheFilenameAppearsInTitle(t *testing.T) {
	table := NewContainerTable(testContainers())
	table.SetCachePath("/home/user/.config/kubernova/clusters/prod/ctx/cache_261105_140322.json")
	title := table.GetTitle()
	if !strings.Contains(title, "Kubernova(all)[6]") {
		t.Errorf("title %q lost the existing shape", title)
	}
	if !strings.Contains(title, "[cache_261105_140322.json]") {
		t.Errorf("title %q does not show the cache filename", title)
	}
}

func TestMoveSortColumnDoesNotChangeSorting(t *testing.T) {
	table := NewContainerTable(testContainers())
	if got := table.MoveSortColumn(1); got != "K02" {
		t.Errorf("MoveSortColumn(1) = %q, want K02", got)
	}
	if got := table.SortColumnID(); got != "" {
		t.Errorf("selecting a column changed the sort column to %q", got)
	}
	if got := cellID(t, table, 1, "K02"); got != "default" {
		t.Errorf("selecting a column reordered rows: %q", got)
	}
	if cell := table.GetCell(0, 0); !strings.Contains(cell.Text, "NAMESPACE") {
		t.Errorf("selected column header = %q", cell.Text)
	}
	if fg, _, attrs := table.GetCell(0, 0).Style.Decompose(); fg != tcell.ColorLightPink || attrs&tcell.AttrBold == 0 {
		t.Errorf("selected column header style = fg %v attrs %v", fg, attrs)
	}

	table.SetSortDescending(true)
	if got := table.SortColumnID(); got != "K02" {
		t.Errorf("sorting selected column = %q, want K02", got)
	}
}

func TestPrometheusColumnsSortNumerically(t *testing.T) {
	cpuLow := 0.02
	cpuHigh := 0.1
	mem := 128.0 * 1024 * 1024
	containers := []model.Container{
		{Namespace: "default", Pod: "low", Name: "low", Prometheus: &model.PrometheusData{CPURequestPercentile: &cpuHigh, MemoryRequestPercentile: &mem}},
		{Namespace: "default", Pod: "high", Name: "high", Prometheus: &model.PrometheusData{CPURequestPercentile: &cpuLow}},
	}
	table := NewContainerTable(containers)
	visibility := make([]bool, len(model.Columns))
	for index := range visibility {
		visibility[index] = true
	}
	table.SetColumnVisibility(visibility)
	table.SetSort("P01", false)
	if got := table.GetCell(1, uiQIndex(t, "P01")).Text; got != "20m" {
		t.Errorf("first numeric P01 cell = %q, want 20m", got)
	}
	if got := table.GetCell(2, uiQIndex(t, "P01")).Text; got != "100m" {
		t.Errorf("second numeric P01 cell = %q, want 100m", got)
	}
	missing := table.GetCell(1, uiQIndex(t, "P05")).Text
	if missing != "-" {
		t.Errorf("missing P05 cell = %q, want -", missing)
	}
}

func TestPrometheusSuggestionColumnsSortBySuggestedValue(t *testing.T) {
	cpuLow := 0.1
	cpuHigh := 0.5
	containers := []model.Container{
		{Namespace: "default", Pod: "high", Name: "high", CPUReq: "100m", Prometheus: &model.PrometheusData{CPURequestSuggestion: &cpuHigh}},
		{Namespace: "default", Pod: "low", Name: "low", CPUReq: "100m", Prometheus: &model.PrometheusData{CPURequestSuggestion: &cpuLow}},
	}
	table := NewContainerTable(containers)
	visibility := make([]bool, len(model.Columns))
	for index := range visibility {
		visibility[index] = true
	}
	table.SetColumnVisibility(visibility)
	table.SetSort("P02", false)
	if got := table.GetCell(1, uiQIndex(t, "P02")).Text; got != "100m -> 100m" {
		t.Errorf("first suggestion cell = %q, want the lower suggestion", got)
	}
	if got := table.GetCell(2, uiQIndex(t, "P02")).Text; got != "100m -> 500m" {
		t.Errorf("second suggestion cell = %q, want the higher suggestion", got)
	}
}

func uiQIndex(t *testing.T, id string) int {
	t.Helper()
	index := ColumnIndex(id)
	if index < 0 {
		t.Fatalf("column %s not found", id)
	}
	return index
}

func allColumnsTable(containers []model.Container) *ContainerTable {
	table := NewContainerTable(containers)
	visibility := make([]bool, len(model.Columns))
	for index := range visibility {
		visibility[index] = true
	}
	table.SetColumnVisibility(visibility)
	return table
}

func TestNaturalSortOrdersEmbeddedNumbers(t *testing.T) {
	containers := []model.Container{
		{Cluster: "c", Namespace: "default", Pod: "pod-10", Name: "a"},
		{Cluster: "c", Namespace: "default", Pod: "pod-2", Name: "b"},
		{Cluster: "c", Namespace: "default", Pod: "pod-1", Name: "c"},
	}
	table := allColumnsTable(containers)
	table.SetSort("K05", false)
	want := []string{"pod-1", "pod-2", "pod-10"}
	for index, expected := range want {
		if got := table.GetCell(index+1, ColumnIndex("K05")).Text; got != expected {
			t.Errorf("natural sort row %d = %q, want %q", index, got, expected)
		}
	}
}

func TestQuantitySortOrdersCPUAndMemory(t *testing.T) {
	tests := []struct {
		column  string
		field   func(*model.Container, string)
		values  []string
		ascend  []string
		descend []string
	}{
		{"K09", func(c *model.Container, v string) { c.CPUReq = v }, []string{"1", "-", "250m", "100m"}, []string{"100m", "250m", "1", "-"}, []string{"1", "250m", "100m", "-"}},
		{"K11", func(c *model.Container, v string) { c.MemReq = v }, []string{"1Gi", "70Mi", "-", "100Mi"}, []string{"70Mi", "100Mi", "1Gi", "-"}, []string{"1Gi", "100Mi", "70Mi", "-"}},
	}
	for _, test := range tests {
		t.Run(test.column, func(t *testing.T) {
			containers := make([]model.Container, len(test.values))
			for index, value := range test.values {
				containers[index] = model.Container{Cluster: "c", Namespace: "default", Pod: "pod", Name: value}
				test.field(&containers[index], value)
			}
			table := allColumnsTable(containers)
			table.SetSort(test.column, false)
			for index, expected := range test.ascend {
				if got := table.GetCell(index+1, ColumnIndex(test.column)).Text; got != expected {
					t.Errorf("ascending row %d = %q, want %q", index, got, expected)
				}
			}
			table.SetSort(test.column, true)
			for index, expected := range test.descend {
				if got := table.GetCell(index+1, ColumnIndex(test.column)).Text; got != expected {
					t.Errorf("descending row %d = %q, want %q", index, got, expected)
				}
			}
		})
	}
}

func TestReplicaSortIsNumericAndMissingIsLast(t *testing.T) {
	containers := []model.Container{
		{Cluster: "c", Namespace: "default", Pod: "a", Name: "a", Replicas: "10"},
		{Cluster: "c", Namespace: "default", Pod: "b", Name: "b", Replicas: "-"},
		{Cluster: "c", Namespace: "default", Pod: "c", Name: "c", Replicas: "2"},
	}
	table := allColumnsTable(containers)
	table.SetSort("K17", false)
	for index, expected := range []string{"2", "10", "-"} {
		if got := table.GetCell(index+1, ColumnIndex("K17")).Text; got != expected {
			t.Errorf("ascending replica row %d = %q, want %q", index, got, expected)
		}
	}
	table.SetSort("K17", true)
	for index, expected := range []string{"10", "2", "-"} {
		if got := table.GetCell(index+1, ColumnIndex("K17")).Text; got != expected {
			t.Errorf("descending replica row %d = %q, want %q", index, got, expected)
		}
	}
}

func TestCompletedRowsStayAtTheBottomUnderEverySort(t *testing.T) {
	containers := []model.Container{
		{Cluster: "c", Namespace: "default", Pod: "done-1", Name: "done", OwnerName: "zzz", Status: &model.ContainerStatus{State: "Terminated", Reason: "Completed"}},
		{Cluster: "c", Namespace: "default", Pod: "active-1", Name: "active", OwnerName: "aaa", Status: &model.ContainerStatus{State: "Running", Running: true}},
		{Cluster: "c", Namespace: "default", Pod: "done-2", Name: "done", OwnerName: "aaa", Status: &model.ContainerStatus{State: "Terminated", Reason: "Completed"}},
		{Cluster: "c", Namespace: "default", Pod: "active-2", Name: "active", OwnerName: "zzz", Status: &model.ContainerStatus{State: "Waiting", Reason: "CrashLoopBackOff"}},
	}
	for _, descending := range []bool{false, true} {
		table := allColumnsTable(containers)
		table.SetReplicaCollapsed(false)
		table.SetSort("K03", descending)
		last := table.GetRowCount() - 1
		for row := 1; row <= 2; row++ {
			if strings.Contains(table.GetCell(row, 0).Text, "completed") {
				t.Errorf("completed row appeared before active rows at row %d (descending=%v)", row, descending)
			}
		}
		for row := last - 1; row <= last; row++ {
			if !strings.Contains(table.GetCell(row, 0).Text, "completed") {
				t.Errorf("row %d is not marked completed (descending=%v): %q", row, descending, table.GetCell(row, 0).Text)
			}
		}
		if got := table.GetCell(last, 0).Text; !strings.Contains(got, "completed") {
			t.Errorf("last row is not completed: %q", got)
		}
		if fg, _, _ := table.GetCell(last, 0).Style.Decompose(); fg != tcell.ColorYellow {
			t.Errorf("completed row color = %v, want yellow", fg)
		}
	}
}

func TestViewportColumnOffsetKeepsSelectionVisible(t *testing.T) {
	if got := viewportColumnOffset(0, 10); got != 0 {
		t.Errorf("viewportColumnOffset(0, 10) = %d, want 0", got)
	}
	if got := viewportColumnOffset(4, 10); got != 4 {
		t.Errorf("viewportColumnOffset(4, 10) = %d, want 4", got)
	}
	if got := viewportColumnOffset(12, 3); got != 2 {
		t.Errorf("viewportColumnOffset(12, 3) = %d, want clamped 2", got)
	}
}

func TestMoveSortColumnScrollsSelectedColumnIntoView(t *testing.T) {
	table := NewContainerTable(testContainers())
	table.MoveSortColumn(1) // Select the first visible column.
	table.MoveSortColumn(3)
	if _, column := table.GetOffset(); column != 3 {
		t.Errorf("horizontal offset = %d, want 3 for the selected column", column)
	}
	table.MoveSortColumn(-1)
	if _, column := table.GetOffset(); column != 2 {
		t.Errorf("horizontal offset = %d, want 2 after moving left", column)
	}
}

func TestColumnVisibilityFromStateIDs(t *testing.T) {
	table := NewContainerTable(testContainers())
	table.SetColumnVisibility(ColumnVisibilityFromIDs([]string{"K19", "K02", "bogus", "K01"}))

	// Columns always render in model order, whatever order the IDs were saved in.
	want := []string{"K02", "K19", "K01"}
	if got := table.GetColumnCount(); got != len(want) {
		t.Fatalf("column count = %d, want %d", got, len(want))
	}
	for position, id := range want {
		if header := table.GetCell(0, position).Text; !strings.Contains(header, id) {
			t.Errorf("header %d = %q, want %s", position, header, id)
		}
	}
	if got := strings.Join(ColumnVisibilityIDs(table.ColumnVisibility()), " "); got != "K02 K19 K01" {
		t.Errorf("ColumnVisibilityIDs() = %q", got)
	}
	if ColumnVisibilityFromIDs(nil) != nil {
		t.Error("nil IDs should keep the current visibility")
	}
}

func TestNoVisibleColumnsDoesNotPanic(t *testing.T) {
	table := NewContainerTable(testContainers())
	table.SetColumnVisibility(ColumnVisibilityFromIDs([]string{}))
	table.SetSort("K02", false)
	table.SetFilter("default")
	if got := table.MoveSortColumn(1); got != "" {
		t.Errorf("MoveSortColumn with no columns = %q, want empty", got)
	}
	table.SetSortDescending(true)
	table.ToggleColumn(ColumnIndex("K02"))
	if got := table.GetColumnCount(); got != 1 {
		t.Errorf("column count after restoring one column = %d, want 1", got)
	}
}
