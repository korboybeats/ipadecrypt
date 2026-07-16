package main

import (
	"testing"

	"github.com/londek/ipadecrypt/internal/appstore"
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

func TestWithStorefrontAccountUsesFreshCredentials(t *testing.T) {
	cfg := &config.Config{Apple: config.Apple{PasswordToken: "old", StoreFront: "143478"}}

	first, err := withStorefrontAccount(cfg, "US", func(acc *appstore.Account) (string, error) {
		return acc.PasswordToken + ":" + acc.StoreFront, nil
	})
	if err != nil {
		t.Fatalf("first account: %v", err)
	}
	if first != "old:143441" {
		t.Fatalf("first account = %q", first)
	}

	cfg.Apple.PasswordToken = "refreshed"
	second, err := withStorefrontAccount(cfg, "US", func(acc *appstore.Account) (string, error) {
		return acc.PasswordToken + ":" + acc.StoreFront, nil
	})
	if err != nil {
		t.Fatalf("second account: %v", err)
	}
	if second != "refreshed:143441" {
		t.Fatalf("second account = %q", second)
	}
}
