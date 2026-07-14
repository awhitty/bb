package ui

import (
	"testing"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

func TestBuildListRowsSectionsAndSpacers(t *testing.T) {
	cols := []rollup.Column{
		{Key: "open", Title: "open", Issues: []bd.Issue{{ID: "a"}, {ID: "b"}}},
		{Key: "wip", Title: "wip", Issues: []bd.Issue{{ID: "c"}}},
	}
	rows := buildListRows(cols)
	// header, a, b, spacer, header, c  (no trailing spacer)
	if len(rows) != 6 {
		t.Fatalf("rows = %d, want 6: %+v", len(rows), rows)
	}
	if rows[0].kind != rowHeader || rows[0].count != 2 || rows[3].kind != rowSpacer {
		t.Fatalf("structure wrong: %+v", rows)
	}
	if rows[len(rows)-1].kind == rowSpacer {
		t.Fatal("must not end on a spacer")
	}
	// issue rows carry (section,row) coordinates matching focus
	if rows[1].col != 0 || rows[1].row != 0 || rows[5].col != 1 || rows[5].row != 0 {
		t.Fatalf("coords wrong: %+v", rows)
	}
}

func TestListNavFlowsAcrossSections(t *testing.T) {
	const w, h = 100, 24
	issues := []bd.Issue{
		{ID: "a", Status: "open", IssueType: "task"},
		{ID: "b", Status: "closed", IssueType: "bug"},
	}
	m := testModel(t, issues, w, h)
	m.setMode(rollup.ModeType) // two sections: task{a}, bug{b} (or by count)
	// focus starts at section 0 row 0
	m.colIdx, m.rowIdx = 0, 0
	before := m.focusedIssue().ID
	m.listDown() // should cross into the next section when the first is exhausted
	m.listDown()
	if f := m.focusedIssue(); f == nil || f.ID == before && len(m.columns) > 1 {
		// after two downs across single-issue sections we should have moved
		if m.colIdx == 0 && m.rowIdx == 0 {
			t.Fatalf("listDown did not advance across sections: col=%d row=%d", m.colIdx, m.rowIdx)
		}
	}
	// h/l jump whole sections
	m.colIdx, m.rowIdx = 0, 0
	m.listSection(1)
	if m.colIdx != 1 {
		t.Fatalf("listSection(1) should jump to section 1, got %d", m.colIdx)
	}
}
