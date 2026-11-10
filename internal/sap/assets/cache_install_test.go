package assets

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func withRequiredFiles(t *testing.T, specs []fileSpec) {
	t.Helper()
	original := requiredFiles
	requiredFiles = specs
	t.Cleanup(func() { requiredFiles = original })
}

func TestWriteAndReadCacheRoundTrip(t *testing.T) {
	payload := []byte("apple-sap-asset-v4")
	digest := sha256.Sum256(payload)
	withRequiredFiles(t, []fileSpec{{name: "tiny", size: len(payload), digest: digest}})

	cacheDir := filepath.Join(t.TempDir(), "apple-assets")
	if err := writeCacheFiles(cacheDir, map[string][]byte{"tiny": payload}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(cacheDir, cacheCompleteName)); err != nil {
		t.Fatalf("missing complete marker: %v", err)
	}

	got, err := readCache(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	// bundleFrom ignores unknown keys; the cache files themselves must match.
	data, err := os.ReadFile(filepath.Join(cacheDir, "tiny"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(payload) {
		t.Fatalf("cached bytes mismatch: %q", data)
	}
	_ = got

	// Re-reading a validated cache must succeed without rewriting.
	if _, err := readCache(cacheDir); err != nil {
		t.Fatal(err)
	}

	// An incomplete staging dir without .complete must not be treated as valid.
	incomplete := filepath.Join(t.TempDir(), "incomplete")
	if err := os.Mkdir(incomplete, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incomplete, "tiny"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCache(incomplete); err == nil {
		t.Fatal("incomplete cache should be rejected")
	}
}

func TestWriteCacheInstallsCompleteMarker(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	if err := writeCache(cacheDir, Bundle{}); err == nil {
		t.Fatal("expected validation error")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, cacheCompleteName)); !os.IsNotExist(err) {
		t.Fatalf("complete marker should not exist after failed write: %v", err)
	}
}
