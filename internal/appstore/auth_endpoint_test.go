package appstore

import "testing"

func TestNormalizeAuthEndpoint(t *testing.T) {
	const fallback = "https://auth.itunes.apple.com/auth/v1/native/fast/"
	if got := normalizeAuthEndpoint(); got != fallback {
		t.Fatalf("normalizeAuthEndpoint() = %q, want %q", got, fallback)
	}
	if got := normalizeAuthEndpoint("https://auth.itunes.apple.com/auth/v1/native"); got != fallback {
		t.Fatalf("normalized endpoint = %q, want %q", got, fallback)
	}
}

func TestAuthEndpointFromText(t *testing.T) {
	text := `redirect=https:\/\/auth.itunes.apple.com\/auth\/v1\/native`
	want := "https://auth.itunes.apple.com/auth/v1/native/fast/"
	if got := authEndpointFromText(text); got != want {
		t.Fatalf("authEndpointFromText() = %q, want %q", got, want)
	}
}

func TestNormalizeNativeAuthEndpointRejectsOtherHosts(t *testing.T) {
	if got := normalizeNativeAuthEndpoint("https://example.com/auth/v1/native"); got != "" {
		t.Fatalf("normalizeNativeAuthEndpoint() = %q, want empty", got)
	}
}
