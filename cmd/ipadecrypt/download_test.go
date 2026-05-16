package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/londek/ipadecrypt/internal/config"
)

func TestDownloadOutputPathDefaultsToConfigRoot(t *testing.T) {
	root := t.TempDir()
	paths := &config.Paths{Root: root}

	got, err := downloadOutputPath(paths, "", "com.example.app", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(root, "com.example.app_1.2.3.ipa")
	if got != want {
		t.Fatalf("downloadOutputPath() = %q, want %q", got, want)
	}
}

func TestDownloadOutputPathUsesDirectoryOverride(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "downloads")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	paths := &config.Paths{Root: root}
	got, err := downloadOutputPath(paths, outDir, "com.example.app", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(outDir, "com.example.app_1.2.3.ipa")
	if got != want {
		t.Fatalf("downloadOutputPath() = %q, want %q", got, want)
	}
}

func TestMultiDownloadOutputDirRejectsFile(t *testing.T) {
	root := t.TempDir()
	outFile := filepath.Join(root, "one.ipa")
	if err := os.WriteFile(outFile, []byte("ipa"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths := &config.Paths{Root: root}
	if _, err := multiDownloadOutputDir(paths, outFile); err == nil {
		t.Fatalf("multiDownloadOutputDir() succeeded for file override")
	}
}

func TestParseStoreTargetArgRejectsLocalIPA(t *testing.T) {
	_, err := parseStoreTargetArg("example.ipa", "download")
	if err == nil {
		t.Fatalf("parseStoreTargetArg() accepted a local IPA")
	}
}
