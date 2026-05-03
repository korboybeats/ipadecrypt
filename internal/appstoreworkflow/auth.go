package appstoreworkflow

import (
	"errors"
	"fmt"

	"github.com/londek/ipadecrypt/internal/appstore"
	"github.com/londek/ipadecrypt/internal/config"
)

type AuthEvent int

const (
	AuthReauth AuthEvent = iota + 1
	AuthLicense
	AuthRetryingDownload
)

func LoginAndSave(cfg *config.Config, as *appstore.Client, email, password, authCode string) error {
	acc, err := as.Login(email, password, authCode)
	if err != nil {
		return err
	}

	cfg.Apple.Email = email
	cfg.Apple.Password = password
	cfg.Apple.Account = acc

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return nil
}

func Reauth(cfg *config.Config, as *appstore.Client) error {
	if cfg.Apple.Email == "" || cfg.Apple.Password == "" {
		return errors.New("missing Apple credentials")
	}

	if err := LoginAndSave(cfg, as, cfg.Apple.Email, cfg.Apple.Password, ""); err != nil {
		return fmt.Errorf("re-auth: %w", err)
	}

	return nil
}

func AcquireLicense(cfg *config.Config, as *appstore.Client, app appstore.App) error {
	err := as.Purchase(cfg.Apple.Account, app)
	if errors.Is(err, appstore.ErrPasswordTokenExpired) {
		if err := Reauth(cfg, as); err != nil {
			return err
		}
		err = as.Purchase(cfg.Apple.Account, app)
	}

	if err != nil && !errors.Is(err, appstore.ErrLicenseAlreadyExists) {
		return fmt.Errorf("purchase: %w", err)
	}

	return nil
}

func WithAuth[T any](cfg *config.Config, as *appstore.Client, app appstore.App, retries int, onEvent func(AuthEvent), fn func() (T, error)) (T, error) {
	var zero T

	notify := func(e AuthEvent) {
		if onEvent != nil {
			onEvent(e)
		}
	}

	for range retries {
		out, err := fn()
		if err == nil {
			return out, nil
		}

		switch {
		case errors.Is(err, appstore.ErrPasswordTokenExpired):
			notify(AuthReauth)
			if err := Reauth(cfg, as); err != nil {
				return zero, err
			}
			notify(AuthRetryingDownload)
		case errors.Is(err, appstore.ErrLicenseRequired):
			notify(AuthLicense)
			if err := AcquireLicense(cfg, as, app); err != nil {
				return zero, err
			}
			notify(AuthRetryingDownload)
		default:
			return zero, err
		}
	}

	return zero, errors.New("exhausted retries")
}
