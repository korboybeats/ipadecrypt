package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssetNameForPlatform(t *testing.T) {
	tests := []struct {
		goos   string
		goarch string
		want   string
	}{
		{"linux", "amd64", "ipadecrypt_0.6.2-korboy.1_linux_amd64"},
		{"linux", "arm64", "ipadecrypt_0.6.2-korboy.1_linux_arm64"},
		{"darwin", "arm64", "ipadecrypt_0.6.2-korboy.1_darwin_arm64"},
		{"windows", "amd64", "ipadecrypt_0.6.2-korboy.1_windows_amd64.exe"},
	}

	for _, tt := range tests {
		got := assetNameForPlatform("v0.6.2-korboy.1", tt.goos, tt.goarch)
		if got != tt.want {
			t.Fatalf("assetNameForPlatform(%q, %q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestFindAsset(t *testing.T) {
	rel := &Release{Assets: []Asset{
		{Name: "checksums.txt", BrowserDownloadURL: "https://example.invalid/checksums.txt"},
		{Name: "ipadecrypt_0.6.2-korboy.1_linux_amd64", BrowserDownloadURL: "https://example.invalid/ipadecrypt"},
	}}

	got, ok := rel.FindAsset("ipadecrypt_0.6.2-korboy.1_linux_amd64")
	if !ok {
		t.Fatal("FindAsset did not find existing asset")
	}
	if got.BrowserDownloadURL == "" {
		t.Fatal("FindAsset returned empty download URL")
	}

	if _, ok := rel.FindAsset("missing"); ok {
		t.Fatal("FindAsset found missing asset")
	}
}

func TestSelectLatestReleaseUsesHighestVersionTag(t *testing.T) {
	releases := []Release{
		{Tag: "v0.6.1-korboy1", Assets: []Asset{{Name: "checksums.txt"}}},
		{Tag: "v0.6.2-korboy.1", Assets: []Asset{{Name: "checksums.txt"}}},
		{Tag: "not-semver", Assets: []Asset{{Name: "checksums.txt"}}},
		{Tag: "v9.0.0", Draft: true, Assets: []Asset{{Name: "checksums.txt"}}},
	}

	got, err := selectLatestRelease(releases)
	if err != nil {
		t.Fatalf("selectLatestRelease: %v", err)
	}
	if got.Tag != "v0.6.2-korboy.1" {
		t.Fatalf("latest = %q, want v0.6.2-korboy.1", got.Tag)
	}
}

func TestParseChecksums(t *testing.T) {
	data := []byte("abc123  ipadecrypt_0.6.2-korboy.1_linux_amd64\nffff  checksums.txt\n")
	got := parseChecksums(data)
	if got["ipadecrypt_0.6.2-korboy.1_linux_amd64"] != "abc123" {
		t.Fatalf("checksum = %q, want abc123", got["ipadecrypt_0.6.2-korboy.1_linux_amd64"])
	}
}

func TestVerifySHA256RejectsMismatch(t *testing.T) {
	err := verifySHA256([]byte("payload"), "0000")
	if err == nil {
		t.Fatal("verifySHA256 accepted a mismatch")
	}
}

func TestReplaceExecutableCreatesBackup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "ipadecrypt")
	backup := target + ".bak"

	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	if err := replaceExecutable(target, backup, []byte("new")); err != nil {
		t.Fatalf("replaceExecutable: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("target = %q, want new", got)
	}

	old, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(old) != "old" {
		t.Fatalf("backup = %q, want old", old)
	}
}

func TestRollbackExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "ipadecrypt")
	backup := target + ".bak"

	if err := os.WriteFile(target, []byte("new"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(backup, []byte("old"), 0o755); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	if err := rollbackExecutable(target, backup); err != nil {
		t.Fatalf("rollbackExecutable: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "old" {
		t.Fatalf("target = %q, want old", got)
	}
}
