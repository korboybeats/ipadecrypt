package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadOnlyMigratesInMemoryWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"version":1,"apple":{"email":"test@example.com","password":"password","account":{"passwordToken":"token","directoryServicesIdentifier":"123","storeFront":"143441-1,29"}}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadReadOnly(path)
	if err != nil {
		t.Fatalf("LoadReadOnly: %v", err)
	}
	if cfg.Version != SchemaVersion || cfg.Apple.PasswordToken != "token" {
		t.Fatalf("config not migrated in memory: %+v", cfg.Apple)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(after) != string(original) {
		t.Fatal("LoadReadOnly modified the config file")
	}
}

func TestNormalizeOutputKeep(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", OutputKeepBoth},
		{"both", OutputKeepBoth},
		{"desktop", OutputKeepDesktop},
		{"local", OutputKeepDesktop},
		{"pc", OutputKeepDesktop},
		{"computer", OutputKeepDesktop},
		{"device", OutputKeepDevice},
		{"phone", OutputKeepDevice},
	}

	for _, tt := range tests {
		got, err := NormalizeOutputKeep(tt.in)
		if err != nil {
			t.Fatalf("NormalizeOutputKeep(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("NormalizeOutputKeep(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeOutputKeepRejectsInvalid(t *testing.T) {
	if _, err := NormalizeOutputKeep("remote"); err == nil {
		t.Fatal("NormalizeOutputKeep accepted invalid value")
	}
}

func TestConfigOutputKeepDefaultsToBoth(t *testing.T) {
	cfg := New("")
	if got := cfg.OutputKeep(); got != OutputKeepBoth {
		t.Fatalf("OutputKeep() = %q, want %q", got, OutputKeepBoth)
	}
}
