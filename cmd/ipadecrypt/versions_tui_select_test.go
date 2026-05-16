package main

import "testing"

func TestVersionsUISelectedExtVerIDsPreservesDisplayOrder(t *testing.T) {
	ui := &versionsUI{
		rows: []versionsRow{
			{extVerID: "newest"},
			{extVerID: "middle"},
			{extVerID: "oldest"},
		},
		selected: map[string]struct{}{
			"oldest": {},
			"newest": {},
		},
	}

	got := ui.selectedExtVerIDs()
	want := []string{"newest", "oldest"}

	if len(got) != len(want) {
		t.Fatalf("selectedExtVerIDs() len = %d, want %d (%v)", len(got), len(want), got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selectedExtVerIDs()[%d] = %q, want %q (%v)", i, got[i], want[i], got)
		}
	}
}

func TestVersionsUIToggleSelected(t *testing.T) {
	ui := &versionsUI{
		cursor: 1,
		rows: []versionsRow{
			{extVerID: "newest"},
			{extVerID: "middle"},
			{extVerID: "oldest"},
		},
		selected: map[string]struct{}{},
	}

	ui.toggleSelected()
	if _, ok := ui.selected["middle"]; !ok {
		t.Fatalf("toggleSelected() did not select cursor row")
	}

	ui.toggleSelected()
	if _, ok := ui.selected["middle"]; ok {
		t.Fatalf("toggleSelected() did not deselect cursor row")
	}
}

func TestVersionsUIToggleSelectedSingleSelectionClearsPrevious(t *testing.T) {
	ui := &versionsUI{
		cursor:         1,
		selectionLimit: 1,
		rows: []versionsRow{
			{extVerID: "newest"},
			{extVerID: "middle"},
			{extVerID: "oldest"},
		},
		selected: map[string]struct{}{
			"newest": {},
		},
	}

	ui.toggleSelected()

	if _, ok := ui.selected["newest"]; ok {
		t.Fatalf("toggleSelected() kept previous single-selection row")
	}
	if _, ok := ui.selected["middle"]; !ok {
		t.Fatalf("toggleSelected() did not select cursor row")
	}
}
