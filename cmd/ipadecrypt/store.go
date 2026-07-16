package main

import (
	"github.com/londek/ipadecrypt/internal/appstore"
	"github.com/londek/ipadecrypt/internal/appstoreworkflow"
	"github.com/londek/ipadecrypt/internal/config"
)

// authEvent names recovery steps that withAuth takes so callers can
// drive their UI. The store helpers themselves stay UI-ignorant.
type authEvent int

const (
	authReauth           authEvent = iota + 1 // re-authenticating because the token expired
	authLicense                               // acquiring a license before retrying
	authRetryingDownload                      // kicking the call off again
)

// accountWithStorefront returns the configured Apple account, overriding the
// storefront when flag is non-empty.
func accountWithStorefront(cfg *config.Config, flag string) (*appstore.Account, error) {
	acc := cfg.Apple.Account()
	if flag == "" {
		return acc, nil
	}

	sf, err := appstore.ResolveStorefront(flag)
	if err != nil {
		return nil, err
	}

	acc.StoreFront = sf

	return acc, nil
}

// withAuth runs fn with up to `retries` attempts, recovering from the two
// well-known recoverable errors from the private App Store endpoint:
// ErrPasswordTokenExpired via reauth and ErrLicenseRequired via
// acquireLicense. Any other error returns immediately.
func withAuth[T any](cfg *config.Config, as *appstore.Client, app appstore.App, retries int, onEvent func(authEvent), fn func() (T, error)) (T, error) {
	return appstoreworkflow.WithAuth(cfg, as, app, retries, func(e appstoreworkflow.AuthEvent) {
		if onEvent != nil {
			onEvent(authEvent(e))
		}
	}, fn)
}
