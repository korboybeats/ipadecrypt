package main

import (
	"reflect"
	"testing"
)

func TestNewestFirstReversesAppleVersionOrder(t *testing.T) {
	got := newestFirst([]string{"old", "middle", "new"})
	want := []string{"new", "middle", "old"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("newestFirst() = %#v, want %#v", got, want)
	}
}

func TestNewestFirstDoesNotMutateInput(t *testing.T) {
	ids := []string{"old", "new"}
	_ = newestFirst(ids)
	if !reflect.DeepEqual(ids, []string{"old", "new"}) {
		t.Fatalf("newestFirst mutated input: %#v", ids)
	}
}
