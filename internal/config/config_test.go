package config

import "testing"

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
