package main

import "testing"

func TestNewUpdateCommand(t *testing.T) {
	cmd := newUpdateCommand()
	if cmd.Use != "update" {
		t.Fatalf("Use = %q, want update", cmd.Use)
	}
	if cmd.Flag("check") == nil {
		t.Fatal("missing --check flag")
	}
	if cmd.Flag("rollback") == nil {
		t.Fatal("missing --rollback flag")
	}
}
