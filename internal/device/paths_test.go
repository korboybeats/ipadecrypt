package device

import (
	"path"
	"testing"
)

func TestRemoteRootUsesDocumentsWorkspace(t *testing.T) {
	if RemoteRoot != "/var/mobile/Documents/ipadecrypt" {
		t.Fatalf("RemoteRoot = %q, want /var/mobile/Documents/ipadecrypt", RemoteRoot)
	}
	if LegacyRemoteRoot != "/var/mobile/Media/ipadecrypt" {
		t.Fatalf("LegacyRemoteRoot = %q, want /var/mobile/Media/ipadecrypt", LegacyRemoteRoot)
	}
}

func TestHelperPathUsesDocumentsWorkspace(t *testing.T) {
	got := helperRemotePath("abc123")
	want := "/var/mobile/Documents/ipadecrypt/helpers/ipadecrypt-helper-arm64-abc123.bin"
	if got != want {
		t.Fatalf("helperRemotePath() = %q, want %q", got, want)
	}
	if path.Dir(got) != "/var/mobile/Documents/ipadecrypt/helpers" {
		t.Fatalf("helper dir = %q", path.Dir(got))
	}
}
