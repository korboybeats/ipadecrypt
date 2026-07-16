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

func TestSavedAppleAuthAvailable(t *testing.T) {
	complete := &config.Config{Apple: config.Apple{
		Email:                       "test@example.com",
		Password:                    "password",
		PasswordToken:               "token",
		DirectoryServicesIdentifier: "123",
		StoreFront:                  "143441-1,29",
	}}
	if !savedAppleAuthAvailable(complete) {
		t.Fatal("complete saved auth reported unavailable")
	}

	tests := []struct {
		name string
		cfg  *config.Config
	}{
		{name: "nil config", cfg: nil},
		{name: "missing email", cfg: &config.Config{Apple: config.Apple{Password: "password", PasswordToken: "token", DirectoryServicesIdentifier: "123", StoreFront: "143441-1,29"}}},
		{name: "missing password", cfg: &config.Config{Apple: config.Apple{Email: "test@example.com", PasswordToken: "token", DirectoryServicesIdentifier: "123", StoreFront: "143441-1,29"}}},
		{name: "missing token", cfg: &config.Config{Apple: config.Apple{Email: "test@example.com", Password: "password", DirectoryServicesIdentifier: "123", StoreFront: "143441-1,29"}}},
		{name: "missing directory ID", cfg: &config.Config{Apple: config.Apple{Email: "test@example.com", Password: "password", PasswordToken: "token", StoreFront: "143441-1,29"}}},
		{name: "missing storefront", cfg: &config.Config{Apple: config.Apple{Email: "test@example.com", Password: "password", PasswordToken: "token", DirectoryServicesIdentifier: "123"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if savedAppleAuthAvailable(tt.cfg) {
				t.Fatal("incomplete saved auth reported available")
			}
		})
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

func TestRefreshApplicationRegistrationUsesFirstExistingCandidate(t *testing.T) {
	var gotName string
	var gotArgs []string
	err := refreshApplicationRegistration(
		"/apps/Test.app",
		[]string{"/missing", "/uicache"},
		func(path string) bool { return path == "/uicache" },
		func(name string, args ...string) error {
			gotName = name
			gotArgs = append([]string(nil), args...)
			return nil
		},
	)

	if err != nil {
		t.Fatalf("refreshApplicationRegistration returned error: %v", err)
	}
	if gotName != "/uicache" || !reflect.DeepEqual(gotArgs, []string{"-p", "/apps/Test.app"}) {
		t.Fatalf("command = %q %#v, want /uicache -p /apps/Test.app", gotName, gotArgs)
	}
}

func TestRefreshApplicationRegistrationRequiresUICache(t *testing.T) {
	called := false
	err := refreshApplicationRegistration(
		"/apps/Test.app",
		[]string{"/missing"},
		func(string) bool { return false },
		func(string, ...string) error {
			called = true
			return nil
		},
	)

	if err == nil || err.Error() != "uicache not found" {
		t.Fatalf("error = %v, want uicache not found", err)
	}
	if called {
		t.Fatal("command ran without a uicache executable")
	}
}

func TestRefreshApplicationRegistrationTriesNextCandidateAfterFailure(t *testing.T) {
	var attempted []string
	err := refreshApplicationRegistration(
		"/apps/Test.app",
		[]string{"/broken-uicache", "/working-uicache"},
		func(string) bool { return true },
		func(name string, _ ...string) error {
			attempted = append(attempted, name)
			if name == "/broken-uicache" {
				return errors.New("broken")
			}
			return nil
		},
	)

	if err != nil {
		t.Fatalf("refreshApplicationRegistration returned error: %v", err)
	}
	want := []string{"/broken-uicache", "/working-uicache"}
	if !reflect.DeepEqual(attempted, want) {
		t.Fatalf("attempted = %#v, want %#v", attempted, want)
	}
}

func TestRefreshApplicationRegistrationReturnsCommandFailure(t *testing.T) {
	want := errors.New("command failed")
	err := refreshApplicationRegistration(
		"/apps/Test.app",
		[]string{"/uicache"},
		func(string) bool { return true },
		func(string, ...string) error { return want },
	)

	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped command failure", err)
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
