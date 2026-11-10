package ui

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"k8s.io/apimachinery/pkg/api/resource"

	"kubernova/internal/model"
)

// ContainerTable is a selectable table of pod containers with a live filter.
type ContainerTable struct {
	*tview.Table

	containers      []model.Container
	filter          string
	namespace       string
	rowKeys         map[int]string
	visible         []bool
	hidden          map[string]bool
	collapseReplica bool
	sortColumn      int
	selectedColumn  int
	sortDescending  bool
	cachePath       string
}

// NewContainerTable creates a container table.
func NewContainerTable(containers []model.Container) *ContainerTable {
	t := &ContainerTable{
		Table:           tview.NewTable(),
		containers:      containers,
		namespace:       "all",
		visible:         make([]bool, len(model.Headers)),
		hidden:          make(map[string]bool),
		collapseReplica: true,
		sortColumn:      -1,
		selectedColumn:  -1,
	}
	for i := range t.visible {
		t.visible[i] = !model.Columns[i].HiddenByDefault
	}

	t.SetSelectable(true, false) // Rows can be selectable.
	t.SetFixed(1, 0)             // Keep the header visible.
	t.SetBorder(true)
	t.SetBackgroundColor(BackgroundColor)
	t.SetSelectedStyle(tcell.StyleDefault.
		Background(tcell.ColorAqua).Foreground(tcell.ColorBlack))

	t.render()

	return t
}

// SetFilter filters the table to containers matching query. Matching is a
// case-insensitive substring search across all visible columns. An empty query
// shows every container.
func (t *ContainerTable) SetFilter(query string) {
	t.filter = query
	t.render()
}

// Filter returns the current filter query.
func (t *ContainerTable) Filter() string {
	return t.filter
}

// ToggleColumn shows or hides the column at index.
func (t *ContainerTable) ToggleColumn(index int) {
	if index < 0 || index >= len(t.visible) {
		return
	}
	t.visible[index] = !t.visible[index]
	t.render()
}

// SetColumnVisible shows or hides the column at index.
func (t *ContainerTable) SetColumnVisible(index int, visible bool) {
	if index < 0 || index >= len(t.visible) {
		return
	}
	t.visible[index] = visible
	t.render()
}

// SetColumnVisibility applies a complete column visibility snapshot.
func (t *ContainerTable) SetColumnVisibility(visible []bool) {
	for index := range t.visible {
		if index < len(visible) {
			t.visible[index] = visible[index]
		}
	}
	t.render()
}

// ColumnVisibility returns a copy of the current column visibility settings.
func (t *ContainerTable) ColumnVisibility() []bool {
	visibility := make([]bool, len(t.visible))
	copy(visibility, t.visible)
	return visibility
}

// ColumnID returns the stable column identifier shown in the header and the
// column picker, for example K01.
func ColumnID(index int) string {
	if index < 0 || index >= len(model.Columns) {
		return ""
	}
	return model.Columns[index].ID
}

// ColumnIndex resolves a stable ID or a historical header label to a column
// index. It returns -1 when the value is unknown.
func ColumnIndex(value string) int {
	value = strings.ToUpper(strings.TrimSpace(value))
	for index, column := range model.Columns {
		if column.ID == value || column.Header == value {
			return index
		}
	}
	// Preserve state written when Prometheus columns used Q identifiers.
	if strings.HasPrefix(value, "Q") {
		legacy := "P" + strings.TrimPrefix(value, "Q")
		for index, column := range model.Columns {
			if column.ID == legacy {
				return index
			}
		}
	}
	return -1
}

// ColumnVisibilityFromIDs converts stable column IDs into a visibility
// snapshot. Unknown values are ignored. A nil input returns nil so callers can
// keep their current visibility.
func ColumnVisibilityFromIDs(ids []string) []bool {
	if ids == nil {
		return nil
	}
	visible := make([]bool, len(model.Columns))
	for _, id := range ids {
		if index := ColumnIndex(id); index >= 0 {
			visible[index] = true
		}
	}
	return visible
}

// ColumnVisibilityIDs returns the stable IDs of visible columns.
func ColumnVisibilityIDs(visible []bool) []string {
	ids := make([]string, 0, len(visible))
	for index, shown := range visible {
		if shown && index < len(model.Columns) {
			ids = append(ids, model.Columns[index].ID)
		}
	}
	return ids
}

// ColumnVisible reports whether the column at index is currently shown.
func (t *ContainerTable) ColumnVisible(index int) bool {
	return index >= 0 && index < len(t.visible) && t.visible[index]
}

// SetSort sorts by a stable column ID. An unknown or empty ID clears sorting
// and restores the incoming row order.
func (t *ContainerTable) SetSort(columnID string, descending bool) {
	index := ColumnIndex(columnID)
	if strings.TrimSpace(columnID) == "" || index < 0 {
		t.sortColumn = -1
		t.selectedColumn = -1
		t.sortDescending = false
	} else {
		t.sortColumn = index
		t.selectedColumn = index
		t.sortDescending = descending
	}
	t.render()
}

// SetSortDescending sorts by the selected column. When no column is selected,
// the first visible column is selected first.
func (t *ContainerTable) SetSortDescending(descending bool) {
	if t.selectedColumn < 0 {
		t.selectedColumn = t.sortColumn
	}
	if t.selectedColumn < 0 {
		columns := t.visibleColumns()
		if len(columns) == 0 {
			return
		}
		t.selectedColumn = columns[0]
	}
	t.sortColumn = t.selectedColumn
	t.sortDescending = descending
	t.render()
}

// ToggleSortDirection flips the current sort direction, selecting the first
// visible column when none is active.
func (t *ContainerTable) ToggleSortDirection() {
	t.SetSortDescending(!t.sortDescending)
}

// MoveSortColumn moves the highlighted column selection to the previous or
// next visible column without changing the current sort. It returns the newly
// selected stable column ID.
func (t *ContainerTable) MoveSortColumn(delta int) string {
	columns := t.visibleColumns()
	if len(columns) == 0 {
		return ""
	}
	position := 0
	for index, column := range columns {
		if column == t.selectedColumn {
			position = index
			break
		}
	}
	if t.selectedColumn < 0 {
		position = 0
		if delta < 0 {
			position = len(columns) - 1
		}
	} else {
		position = (position + delta) % len(columns)
		if position < 0 {
			position += len(columns)
		}
	}
	t.selectedColumn = columns[position]
	rowOffset, _ := t.GetOffset()
	t.SetOffset(rowOffset, viewportColumnOffset(position, len(columns)))
	t.render()
	return ColumnID(t.selectedColumn)
}

// viewportColumnOffset returns the first visible column index that guarantees
// the selected column is inside the viewport. tview keeps its computed
// viewport width internal, so the selected column is aligned to the left edge
// whenever navigation moves the selection.
func viewportColumnOffset(selected, total int) int {
	if total <= 0 || selected <= 0 {
		return 0
	}
	if selected >= total {
		return total - 1
	}
	return selected
}

// SortColumnID returns the stable ID of the sorted column, or an empty string.
func (t *ContainerTable) SortColumnID() string {
	if t.sortColumn < 0 || t.sortColumn >= len(model.Columns) {
		return ""
	}
	return ColumnID(t.sortColumn)
}

// SortDescending reports whether the current sort is descending.
func (t *ContainerTable) SortDescending() bool {
	return t.sortDescending
}

// SetContainers replaces the container rows and re-renders the table.
func (t *ContainerTable) SetContainers(containers []model.Container) {
	t.containers = containers
	t.render()
}

// SetNamespace filters the table to one namespace. An empty value, "-A", or
// "all" shows every namespace.
func (t *ContainerTable) SetNamespace(namespace string) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" || namespace == "-A" || strings.EqualFold(namespace, "all") {
		namespace = "all"
	}
	t.namespace = namespace
	t.render()
}

// Namespace returns the active namespace filter, or "all".
func (t *ContainerTable) Namespace() string {
	if t.namespace == "" {
		return "all"
	}
	return t.namespace
}

// HiddenKeys returns the keys of rows hidden with <space>.
func (t *ContainerTable) HiddenKeys() []string {
	keys := make([]string, 0, len(t.hidden))
	for key, hidden := range t.hidden {
		if hidden {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// SetHiddenKeys replaces the hidden row set.
func (t *ContainerTable) SetHiddenKeys(keys []string) {
	t.hidden = make(map[string]bool, len(keys))
	for _, key := range keys {
		t.hidden[key] = true
	}
	t.render()
}

// ToggleSelectedHidden hides or restores the selected row. Hidden rows are
// rendered gray and sorted to the end of the table. The selection stays at the
// same table position instead of following the moved row.
func (t *ContainerTable) ToggleSelectedHidden() bool {
	row, _ := t.GetSelection()
	key := t.rowKeys[row]
	if key == "" {
		return false
	}
	if t.hidden == nil {
		t.hidden = make(map[string]bool)
	}
	t.hidden[key] = !t.hidden[key]
	t.render()

	if row > 0 && t.GetRowCount() > 1 {
		if row >= t.GetRowCount() {
			row = t.GetRowCount() - 1
		}
		t.Select(row, 0)
	}
	return true
}

// ToggleReplicaCollapse expands or collapses replica pods.
func (t *ContainerTable) ToggleReplicaCollapse() {
	t.collapseReplica = !t.collapseReplica
	t.render()
}

// SetReplicaCollapsed expands or collapses replica pods.
func (t *ContainerTable) SetReplicaCollapsed(collapsed bool) {
	t.collapseReplica = collapsed
	t.render()
}

// ReplicaCollapsed reports whether replica pods are collapsed.
func (t *ContainerTable) ReplicaCollapsed() bool {
	return t.collapseReplica
}

func (t *ContainerTable) render() {
	selected := t.selectedKey()
	columns := t.visibleColumns()

	var match *regexp.Regexp
	if query := strings.TrimSpace(t.filter); query != "" {
		match = regexp.MustCompile("(?i)" + regexp.QuoteMeta(query))
	}

	entries := make([]containerEntry, 0, len(t.containers))
	for _, container := range t.containers {
		if !t.namespaceAllows(container) || !matches(container, match, columns) {
			continue
		}
		key := containerKey(container)
		entries = append(entries, containerEntry{
			container: container,
			key:       key,
			pod:       podKey(container),
			group:     replicaGroupKey(container),
			hidden:    t.hidden[key],
			completed: container.Completed(),
		})
	}

	representatives := make(map[string]string)
	if t.collapseReplica {
		for _, entry := range entries {
			if entry.hidden {
				continue
			}
			if _, ok := representatives[entry.group]; !ok {
				representatives[entry.group] = entry.pod
			}
		}
	}

	active := make([]containerEntry, 0, len(entries))
	completed := make([]containerEntry, 0, len(entries))
	hidden := make([]containerEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.hidden {
			hidden = append(hidden, entry)
			continue
		}
		if t.collapseReplica && representatives[entry.group] != entry.pod {
			continue
		}
		if entry.completed {
			completed = append(completed, entry)
			continue
		}
		active = append(active, entry)
	}
	sortEntries := func(group []containerEntry) {
		if t.sortColumn < 0 || t.sortColumn >= len(model.Columns) {
			return
		}
		columnID := model.Columns[t.sortColumn].ID
		sort.SliceStable(group, func(i, j int) bool {
			left := group[i].container.Row()[t.sortColumn]
			right := group[j].container.Row()[t.sortColumn]
			if comparison := compareCells(left, right, columnID, t.sortDescending); comparison != 0 {
				return comparison < 0
			}
			return tieBreak(group[i].container, group[j].container) < 0
		})
	}
	sortEntries(active)
	sortEntries(completed)

	// Successfully completed containers always follow active containers and
	// hidden rows always follow both groups.
	ordered := make([]containerEntry, 0, len(entries))
	ordered = append(ordered, active...)
	ordered = append(ordered, completed...)
	ordered = append(ordered, hidden...)

	t.Clear()
	t.rowKeys = make(map[int]string, len(ordered))

	// Header row. Hidden columns disappear completely, including their column
	// identifier, and the remaining columns close the gap.
	for position, column := range columns {
		text := model.Headers[column]
		if column == t.sortColumn {
			if t.sortDescending {
				text += " ▼"
			} else {
				text += " ▲"
			}
		}
		headerColor := tcell.ColorFuchsia
		headerAttributes := tcell.AttrNone
		if column == t.selectedColumn {
			headerColor = tcell.ColorLightPink
			headerAttributes = tcell.AttrBold
		}
		id := ColumnID(column)
		cell := tview.NewTableCell("[gray]" + id + "[-] " + text).
			SetExpansion(1).
			SetTextColor(headerColor).
			SetSelectable(false)
		if headerAttributes != tcell.AttrNone {
			cell.SetAttributes(headerAttributes)
		}
		t.SetCell(0, position, cell)
	}

	// Data rows
	row := 1
	selectedRow := 1
	for _, entry := range ordered {
		values := entry.container.Row()
		for position, column := range columns {
			value := values[column]
			if entry.completed && position == 0 {
				value += " [completed]"
			}
			cell := tview.NewTableCell(tview.Escape(value)).SetExpansion(1)
			switch {
			case entry.hidden:
				cell.SetTextColor(tcell.ColorGray)
			case entry.completed:
				cell.SetText(highlight(value, match)).SetTextColor(tcell.ColorYellow)
			default:
				cell.SetText(highlight(value, match))
			}
			t.SetCell(row, position, cell)
		}
		t.rowKeys[row] = entry.key
		if selected != "" && entry.key == selected {
			selectedRow = row
		}
		row++
	}

	t.updateTitle(len(ordered))

	if row > 1 && len(columns) > 0 {
		selectionColumn := 0
		for position, column := range columns {
			if column == t.selectedColumn {
				selectionColumn = position
				break
			}
		}
		t.Select(selectedRow, selectionColumn)
	}
}

// SetCachePath records the cache file backing the current rows. The table title
// shows only its filename to keep the existing title shape.
func (t *ContainerTable) SetCachePath(path string) {
	t.cachePath = path
	t.render()
}

// CachePath returns the cache file backing the current rows.
func (t *ContainerTable) CachePath() string {
	return t.cachePath
}

func (t *ContainerTable) updateTitle(rowCount int) {
	namespace := t.Namespace()
	title := fmt.Sprintf(" Kubernova(%s)[%d] ", tview.Escape(namespace), rowCount)
	if t.filter != "" {
		query := strings.TrimSpace(t.filter)
		title = fmt.Sprintf(" Kubernova(%s)[%d] [%s] ", tview.Escape(namespace), rowCount, tview.Escape(query))
	}
	if t.cachePath != "" {
		title = strings.TrimSuffix(title, " ")
		title += " [" + tview.Escape(filepath.Base(t.cachePath)) + "] "
	}
	t.SetTitle(title)
}

func (t *ContainerTable) namespaceAllows(container model.Container) bool {
	namespace := t.Namespace()
	return namespace == "all" || container.Namespace == namespace
}

type containerEntry struct {
	container model.Container
	key       string
	pod       string
	group     string
	hidden    bool
	completed bool
}

func (t *ContainerTable) visibleColumns() []int {
	columns := make([]int, 0, len(t.visible))
	for index, visible := range t.visible {
		if visible {
			columns = append(columns, index)
		}
	}
	return columns
}

// selectedKey identifies the currently selected container so it can remain
// selected after the table is filtered.
func (t *ContainerTable) selectedKey() string {
	row, _ := t.GetSelection()
	return t.rowKeys[row]
}

func containerKey(container model.Container) string {
	return podKey(container) + "/" + container.Name
}

func podKey(container model.Container) string {
	return container.Cluster + "/" + container.Namespace + "/" + container.Pod
}

func replicaGroupKey(container model.Container) string {
	if container.OwnerName == "" {
		return podKey(container)
	}
	return container.Cluster + "/" + container.Namespace + "/" + container.Owners + "/" + container.OwnerName
}

// matches reports whether any visible field of the container contains a match.
func matches(container model.Container, match *regexp.Regexp, columns []int) bool {
	if match == nil {
		return true
	}
	values := container.Row()
	for _, column := range columns {
		if match.MatchString(values[column]) {
			return true
		}
	}
	return false
}

// highlight wraps every match in the given text with a red color tag.
func highlight(text string, match *regexp.Regexp) string {
	if match == nil {
		return tview.Escape(text)
	}

	indexes := match.FindAllStringIndex(text, -1)
	if len(indexes) == 0 {
		return tview.Escape(text)
	}

	var b strings.Builder
	last := 0
	for _, index := range indexes {
		b.WriteString(tview.Escape(text[last:index[0]]))
		b.WriteString("[red]")
		b.WriteString(tview.Escape(text[index[0]:index[1]]))
		b.WriteString("[-]")
		last = index[1]
	}
	b.WriteString(tview.Escape(text[last:]))

	return b.String()
}

// compareCells orders two cells according to the column's data type. Missing
// values always sort last, regardless of direction.
func compareCells(left, right, columnID string, descending bool) int {
	leftMissing, rightMissing := missingCell(left), missingCell(right)
	if leftMissing || rightMissing {
		switch {
		case leftMissing && rightMissing:
			return 0
		case leftMissing:
			return 1
		default:
			return -1
		}
	}

	comparison := 0
	switch {
	case numericColumn(columnID):
		leftValue, leftOK := numericValue(left)
		rightValue, rightOK := numericValue(right)
		if leftOK && rightOK {
			switch {
			case leftValue < rightValue:
				comparison = -1
			case leftValue > rightValue:
				comparison = 1
			}
		} else {
			comparison = naturalCompare(left, right)
		}
	case quantityColumn(columnID):
		leftValue, leftOK := quantityValue(left)
		rightValue, rightOK := quantityValue(right)
		if leftOK && rightOK {
			comparison = leftValue.Cmp(rightValue)
		} else {
			comparison = naturalCompare(left, right)
		}
	default:
		comparison = naturalCompare(left, right)
	}
	if descending {
		comparison = -comparison
	}
	return comparison
}

func numericColumn(columnID string) bool {
	return strings.HasPrefix(columnID, "P") || columnID == "K17"
}

func quantityColumn(columnID string) bool {
	switch columnID {
	case "K09", "K10", "K11", "K12":
		return true
	default:
		return false
	}
}

func missingCell(text string) bool {
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "~"))
	return text == "" || text == "-"
}

func cleanCell(text string) string {
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "~"))
	if arrow := strings.LastIndex(text, "->"); arrow >= 0 {
		text = strings.TrimSpace(text[arrow+2:])
	}
	return text
}

// numericValue parses Prometheus and replica cells using Kubernetes quantity
// syntax so values such as 100m, 70Mi, and 1Gi compare numerically. Suggestion
// cells compare by their suggested value and stale markers are ignored.
func numericValue(text string) (float64, bool) {
	text = cleanCell(text)
	if text == "" || text == "-" {
		return 0, false
	}
	if quantity, err := resource.ParseQuantity(text); err == nil {
		return quantity.AsApproximateFloat64(), true
	}
	value, err := strconv.ParseFloat(text, 64)
	return value, err == nil
}

func quantityValue(text string) (resource.Quantity, bool) {
	text = cleanCell(text)
	quantity, err := resource.ParseQuantity(text)
	if err != nil {
		return resource.Quantity{}, false
	}
	return quantity, true
}

// naturalCompare orders text with embedded numbers by their numeric value, so
// pod-2 sorts before pod-10.
func naturalCompare(left, right string) int {
	left = strings.ToLower(left)
	right = strings.ToLower(right)
	for leftIndex, rightIndex := 0, 0; leftIndex < len(left) && rightIndex < len(right); {
		leftDigit := isDigit(left[leftIndex])
		rightDigit := isDigit(right[rightIndex])
		if leftDigit && rightDigit {
			leftEnd := digitRunEnd(left, leftIndex)
			rightEnd := digitRunEnd(right, rightIndex)
			if comparison := compareDigitRuns(left[leftIndex:leftEnd], right[rightIndex:rightEnd]); comparison != 0 {
				return comparison
			}
			leftIndex, rightIndex = leftEnd, rightEnd
			continue
		}
		if left[leftIndex] != right[rightIndex] {
			if left[leftIndex] < right[rightIndex] {
				return -1
			}
			return 1
		}
		leftIndex++
		rightIndex++
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return 0
	}
}

func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func digitRunEnd(text string, start int) int {
	end := start
	for end < len(text) && isDigit(text[end]) {
		end++
	}
	return end
}

func compareDigitRuns(left, right string) int {
	left = strings.TrimLeft(left, "0")
	right = strings.TrimLeft(right, "0")
	if left == "" {
		left = "0"
	}
	if right == "" {
		right = "0"
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

// tieBreak orders rows that compare equal on the sort column by namespace and
// then pod, always ascending, so a sorted view is deterministic.
func tieBreak(left, right model.Container) int {
	if comparison := naturalCompare(left.Namespace, right.Namespace); comparison != 0 {
		return comparison
	}
	return naturalCompare(left.Pod, right.Pod)
}

// SortContainers returns a copy of the rows in the table's completed-row
// grouping and the requested visible sort order. Hidden rows, filters, and
// namespace selection are intentionally not applied; export uses every row.
func SortContainers(containers []model.Container, columnID string, descending bool) []model.Container {
	sorted := append([]model.Container(nil), containers...)
	index := ColumnIndex(columnID)
	if index < 0 {
		return sorted
	}
	active := make([]model.Container, 0, len(sorted))
	completed := make([]model.Container, 0, len(sorted))
	for _, container := range sorted {
		if container.Completed() {
			completed = append(completed, container)
		} else {
			active = append(active, container)
		}
	}
	sortGroup := func(group []model.Container) {
		sort.SliceStable(group, func(i, j int) bool {
			if comparison := compareCells(group[i].Row()[index], group[j].Row()[index], columnID, descending); comparison != 0 {
				return comparison < 0
			}
			return tieBreak(group[i], group[j]) < 0
		})
	}
	sortGroup(active)
	sortGroup(completed)
	return append(active, completed...)
}
