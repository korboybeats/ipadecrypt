package appstore

import "testing"

func TestResolveStorefront(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "uppercase country", input: "US", want: "143441"},
		{name: "lowercase country", input: "pl", want: "143478"},
		{name: "numeric ID", input: "143441", want: "143441"},
		{name: "trimmed country", input: "  US  ", want: "143441"},
		{name: "unknown", input: "ZZ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveStorefront(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveStorefront(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ResolveStorefront(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
