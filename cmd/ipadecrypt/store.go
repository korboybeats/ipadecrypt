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

// reauth refreshes the App Store password token by logging in again with
// stored credentials. Updates cfg.Apple.Account in place and persists it.
func reauth(cfg *config.Config, as *appstore.Client) error {
	return appstoreworkflow.Reauth(cfg, as)
}

// acquireLicense purchases the app (free apps still need a VPP-style license
// entry). Handles mid-purchase token expiry by re-authenticating once and
// retrying. ErrLicenseAlreadyExists is treated as success.
func acquireLicense(cfg *config.Config, as *appstore.Client, app appstore.App) error {
	return appstoreworkflow.AcquireLicense(cfg, as, app)
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
