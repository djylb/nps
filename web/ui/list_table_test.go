package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The three bootstrap-table lists share list-table.js for the column choice,
// the pinned option column, the floating header and the bottom scroll bar:
// they must start through it and read hidden columns in their details.
func TestListsUseSharedTable(t *testing.T) {
	layout, err := os.ReadFile(filepath.Join("..", "views", "public", "layout.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(layout), `/static/js/list-table.js?v={{.version}}`) {
		t.Error("layout.html does not load list-table.js with a version")
	}
	for _, name := range []string{"client/list.html", "index/list.html", "index/hlist.html"} {
		b, err := os.ReadFile(filepath.Join("..", "views", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.Contains(src, "npsList.init('#table', {") || strings.Contains(src, ".bootstrapTable({") {
			t.Errorf("%s must start its table with npsList.init", name)
		}
		if !strings.Contains(src, "npsList.detailColumns(") {
			t.Errorf("%s: the details do not show the hidden columns", name)
		}
		if strings.Contains(src, "bs.table.hiddenColumns") {
			t.Errorf("%s: the column choice is kept by list-table.js, not the cookie extension", name)
		}
	}
}
