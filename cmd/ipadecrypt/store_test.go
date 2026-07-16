package main

import (
	"testing"

	"github.com/londek/ipadecrypt/internal/config"
)

func TestAccountWithStorefrontOverridesCopy(t *testing.T) {
	cfg := &config.Config{Apple: config.Apple{Email: "test@example.com", StoreFront: "143478"}}
	acc, err := accountWithStorefront(cfg, "US")
	if err != nil {
		t.Fatalf("accountWithStorefront: %v", err)
	}
	if acc.StoreFront != "143441" {
		t.Fatalf("StoreFront = %q, want 143441", acc.StoreFront)
	}
	if cfg.Apple.StoreFront != "143478" {
		t.Fatalf("saved storefront mutated to %q", cfg.Apple.StoreFront)
	}
}
