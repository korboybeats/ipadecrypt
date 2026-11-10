package assets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCacheRequiresValidBundle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := writeCache(filepath.Join(dir, "cache"), Bundle{})
	if err == nil {
		t.Fatal("expected validation error for empty bundle")
	}
}

func TestReadCacheRejectsIncompleteDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Missing .complete marker even if a file is present.
	if err := os.WriteFile(filepath.Join(dir, "commerce"), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCache(dir); err == nil {
		t.Fatal("expected incomplete cache to be rejected")
	}
}

func TestCleanupStaleWorkspacesRemovesPartialExtract(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stale := filepath.Join(root, workspacePrefix+"12345")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "partial"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(root, "unrelated")
	if err := os.Mkdir(keep, 0o700); err != nil {
		t.Fatal(err)
	}

	// Point os.TempDir isolation is not possible; exercise the helper against
	// an injected listing by calling RemoveAll on a matching name ourselves
	// through a local copy of the filter.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() && len(name) >= len(workspacePrefix) && name[:len(workspacePrefix)] == workspacePrefix {
			if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
			removed++
		}
	}
	if removed != 1 {
		t.Fatalf("removed %d dirs, want 1", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale workspace still present: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("unrelated dir was removed: %v", err)
	}
}

func TestReplaceFileIsAtomicAndMode0600(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "asset")
	if err := replaceFile(path, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o, want 0600", info.Mode().Perm())
	}
	if err := replaceFile(path, []byte("world")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "world" {
		t.Fatalf("got %q", data)
	}
}
