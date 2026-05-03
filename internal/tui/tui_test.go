package tui

import (
	"bytes"
	"testing"
)

func TestHeaderPrintsPlainTitle(t *testing.T) {
	var buf bytes.Buffer
	oldOut := Out
	Out = &buf
	defer func() { Out = oldOut }()

	Header("Doctor")

	want := "\n== Doctor ==\n"
	if got := buf.String(); got != want {
		t.Fatalf("Header() = %q, want %q", got, want)
	}
}
