package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/londek/ipadecrypt/internal/device"
)

func TestAppinstUploadRemoteRootHide(t *testing.T) {
	got := device.ResolveRemotePath("roothide", "/var/mobile/Documents/ipadecrypt/staging/app.ipa")
	want := "/rootfs/private/var/mobile/Documents/ipadecrypt/staging/app.ipa"
	if got != want {
		t.Fatalf("deviceInstallUploadRemote() = %q, want %q", got, want)
	}
}

func TestAppinstUploadRemoteRootless(t *testing.T) {
	got := device.ResolveRemotePath("Dopamine", "/var/mobile/Documents/ipadecrypt/staging/app.ipa")
	want := "/var/mobile/Documents/ipadecrypt/staging/app.ipa"
	if got != want {
		t.Fatalf("deviceInstallUploadRemote() = %q, want %q", got, want)
	}
}

func TestRemoteOutputPathRootHide(t *testing.T) {
	got := remoteOutputPath("com.example.App", "1.2.3", "roothide")
	want := "/rootfs/private/var/mobile/Documents/ipadecrypt/decrypted/com.example.App_1.2.3.decrypted.ipa"
	if got != want {
		t.Fatalf("remoteOutputPath() = %q, want %q", got, want)
	}
}

func TestShouldAbandonLocalOutput(t *testing.T) {
	if !shouldAbandonLocalOutput(false) {
		t.Fatal("incomplete output must be abandoned")
	}
	if shouldAbandonLocalOutput(true) {
		t.Fatal("completed output must be retained when later verification fails")
	}
}

func TestLocalOutputCleanupRetainsCompletedUndeliveredTemp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "completed.ipa")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create output: %v", err)
	}

	completed := true
	delivered := false
	cleanups := &cleanupStack{}
	pushLocalOutputCleanup(cleanups, file, path, func() { os.Remove(path) }, &completed, &delivered)
	cleanups.run()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("completed undelivered output was removed: %v", err)
	}
}

func TestLocalOutputCleanupRemovesDeliveredTemp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delivered.ipa")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create output: %v", err)
	}

	completed := true
	delivered := true
	cleanups := &cleanupStack{}
	pushLocalOutputCleanup(cleanups, file, path, func() { os.Remove(path) }, &completed, &delivered)
	cleanups.run()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("delivered temporary output still exists: %v", err)
	}
}
