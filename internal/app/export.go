package app

import (
	"fmt"
	"strings"

	"kubernova/internal/ui"
)

// exportColumnIndexes parses export.columns into stable column indexes.
// "all", empty, or whitespace means every column.
func exportColumnIndexes(spec string) ([]int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.EqualFold(spec, "all") {
		return nil, nil
	}
	parts := strings.FieldsFunc(spec, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	})
	indexes := make([]int, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, part := range parts {
		index := ui.ColumnIndex(part)
		if index < 0 {
			return nil, fmt.Errorf("unknown column %q (use all or column IDs such as K02,K05,P10)", part)
		}
		if seen[index] {
			continue
		}
		seen[index] = true
		indexes = append(indexes, index)
	}
	if len(indexes) == 0 {
		return nil, fmt.Errorf("no export columns selected")
	}
	return indexes, nil
}
