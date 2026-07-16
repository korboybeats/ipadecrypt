package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/londek/ipadecrypt/internal/appstore"
	"github.com/londek/ipadecrypt/internal/config"
)

func TestRefreshAppleAuthUsesSavedCredentials(t *testing.T) {
	cfg := &config.Config{Apple: config.Apple{
		Email:    "saved@example.com",
		Password: "saved-password",
	}}

	var gotEmail, gotPassword, gotCode string
	err := refreshAppleAuth(cfg, "", "", "", func(email, password, authCode string) error {
		gotEmail, gotPassword, gotCode = email, password, authCode
		return nil
	})

	if err != nil {
		t.Fatalf("refreshAppleAuth returned error: %v", err)
	}
	if gotEmail != cfg.Apple.Email || gotPassword != cfg.Apple.Password || gotCode != "" {
		t.Fatalf("login credentials = (%q, %q, %q), want saved credentials", gotEmail, gotPassword, gotCode)
	}
}

func TestRefreshAppleAuthPrefersProvidedCredentials(t *testing.T) {
	cfg := &config.Config{Apple: config.Apple{
		Email:    "saved@example.com",
		Password: "saved-password",
	}}

	var gotEmail, gotPassword, gotCode string
	err := refreshAppleAuth(cfg, "new@example.com", "new-password", "123456", func(email, password, authCode string) error {
		gotEmail, gotPassword, gotCode = email, password, authCode
		return nil
	})

	if err != nil {
		t.Fatalf("refreshAppleAuth returned error: %v", err)
	}
	if gotEmail != "new@example.com" || gotPassword != "new-password" || gotCode != "123456" {
		t.Fatalf("login credentials = (%q, %q, %q), want provided credentials", gotEmail, gotPassword, gotCode)
	}
}

func TestRefreshAppleAuthForwardsCodeWithSavedCredentials(t *testing.T) {
	cfg := &config.Config{Apple: config.Apple{
		Email:    "saved@example.com",
		Password: "saved-password",
	}}

	var gotCode string
	err := refreshAppleAuth(cfg, "", "", "654321", func(_, _, authCode string) error {
		gotCode = authCode
		return nil
	})

	if err != nil {
		t.Fatalf("refreshAppleAuth returned error: %v", err)
	}
	if gotCode != "654321" {
		t.Fatalf("auth code = %q, want 654321", gotCode)
	}
}

func TestRefreshAppleAuthRequiresSavedCredentials(t *testing.T) {
	called := false
	err := refreshAppleAuth(&config.Config{}, "", "", "", func(_, _, _ string) error {
		called = true
		return nil
	})

	if !errors.Is(err, errAuthRequired) {
		t.Fatalf("error = %v, want errAuthRequired", err)
	}
	if called {
		t.Fatal("login called without credentials")
	}
}

func TestRefreshAppleAuthRejectsPartialProvidedCredentials(t *testing.T) {
	cfg := &config.Config{Apple: config.Apple{
		Email:    "saved@example.com",
		Password: "saved-password",
	}}

	for name, credentials := range map[string][2]string{
		"missing email":    {"", "new-password"},
		"missing password": {"new@example.com", ""},
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			err := refreshAppleAuth(cfg, credentials[0], credentials[1], "", func(_, _, _ string) error {
				called = true
				return nil
			})

			if err == nil || err.Error() != "missing Apple ID email or password" {
				t.Fatalf("error = %v, want missing-credentials error", err)
			}
			if called {
				t.Fatal("login called with partial provided credentials")
			}
		})
	}
}

func TestRefreshAppleAuthPropagatesLoginError(t *testing.T) {
	cfg := &config.Config{Apple: config.Apple{
		Email:    "saved@example.com",
		Password: "saved-password",
	}}

	err := refreshAppleAuth(cfg, "", "", "", func(_, _, _ string) error {
		return appstore.ErrAuthCodeRequired
	})

	if !errors.Is(err, appstore.ErrAuthCodeRequired) {
		t.Fatalf("error = %v, want ErrAuthCodeRequired", err)
	}
}

func TestNewestFirstReversesAppleVersionOrder(t *testing.T) {
	got := newestFirst([]string{"old", "middle", "new"})
	want := []string{"new", "middle", "old"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("newestFirst() = %#v, want %#v", got, want)
	}
}

func TestNewestFirstDoesNotMutateInput(t *testing.T) {
	ids := []string{"old", "new"}
	_ = newestFirst(ids)
	if !reflect.DeepEqual(ids, []string{"old", "new"}) {
		t.Fatalf("newestFirst mutated input: %#v", ids)
	}
}
