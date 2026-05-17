package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/londek/ipadecrypt/internal/config"
)

func TestKeepOptionsPutCurrentFirst(t *testing.T) {
	options, values := keepOptions(config.OutputKeepDevice)
	if len(options) != 3 || len(values) != 3 {
		t.Fatalf("keepOptions returned %d options and %d values", len(options), len(values))
	}
	if values[0] != config.OutputKeepDevice {
		t.Fatalf("first value = %q, want current policy %q", values[0], config.OutputKeepDevice)
	}
	if options[0] != "Device only" {
		t.Fatalf("first option = %q, want Device only", options[0])
	}
}

func TestDecryptWorkingOutputPathKeepsDesktopPath(t *testing.T) {
	root := t.TempDir()
	decryptOutput = root
	t.Cleanup(func() { decryptOutput = "" })

	got, cleanup, err := decryptWorkingOutputPath(true, "com.example.App", "1.0")
	if err != nil {
		t.Fatalf("decryptWorkingOutputPath: %v", err)
	}
	if cleanup != nil {
		t.Fatal("desktop output returned cleanup function")
	}

	want := filepath.Join(root, "com.example.App_1.0.decrypted.ipa")
	if got != want {
		t.Fatalf("desktop output = %q, want %q", got, want)
	}
}

func TestDecryptWorkingOutputPathDeviceUsesTemp(t *testing.T) {
	decryptOutput = ""
	got, cleanup, err := decryptWorkingOutputPath(false, "com.example.App", "1.0")
	if err != nil {
		t.Fatalf("decryptWorkingOutputPath: %v", err)
	}
	if cleanup == nil {
		t.Fatal("device output did not return cleanup function")
	}
	if filepath.Dir(got) != os.TempDir() {
		t.Fatalf("device output dir = %q, want temp dir %q", filepath.Dir(got), os.TempDir())
	}

	if err := os.WriteFile(got, []byte("x"), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	cleanup()
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatalf("temp file still exists after cleanup: %v", err)
	}
}
