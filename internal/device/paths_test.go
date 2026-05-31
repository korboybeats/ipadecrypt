package device

import (
	"path"
	"testing"
)

func TestRemoteRootUsesDocumentsWorkspace(t *testing.T) {
	if RemoteRoot != "/var/mobile/Documents/ipadecrypt" {
		t.Fatalf("RemoteRoot = %q, want /var/mobile/Documents/ipadecrypt", RemoteRoot)
	}
	if RootHideRemoteRoot != "/rootfs/private/var/mobile/Documents/ipadecrypt" {
		t.Fatalf("RootHideRemoteRoot = %q, want /rootfs/private/var/mobile/Documents/ipadecrypt", RootHideRemoteRoot)
	}
	if LegacyRemoteRoot != "/var/mobile/Media/ipadecrypt" {
		t.Fatalf("LegacyRemoteRoot = %q, want /var/mobile/Media/ipadecrypt", LegacyRemoteRoot)
	}
}

func TestHelperPathUsesDocumentsWorkspace(t *testing.T) {
	got := helperRemotePath("Dopamine", "abc123")
	want := "/var/mobile/Documents/ipadecrypt/helpers/ipadecrypt-helper-arm64-abc123.bin"
	if got != want {
		t.Fatalf("helperRemotePath() = %q, want %q", got, want)
	}
	if path.Dir(got) != "/var/mobile/Documents/ipadecrypt/helpers" {
		t.Fatalf("helper dir = %q", path.Dir(got))
	}
}

func TestHelperPathUsesRootHideWorkspace(t *testing.T) {
	got := helperRemotePath("roothide", "abc123")
	want := "/rootfs/private/var/mobile/Documents/ipadecrypt/helpers/ipadecrypt-helper-arm64-abc123.bin"
	if got != want {
		t.Fatalf("helperRemotePath() = %q, want %q", got, want)
	}
}

func TestResolveRemotePathRootHide(t *testing.T) {
	got := ResolveRemotePath("roothide", "/var/mobile/Documents/ipadecrypt/staging/app.ipa")
	want := "/rootfs/private/var/mobile/Documents/ipadecrypt/staging/app.ipa"
	if got != want {
		t.Fatalf("ResolveRemotePath() = %q, want %q", got, want)
	}
}
