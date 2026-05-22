package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/londek/ipadecrypt/internal/appstore"
	"github.com/londek/ipadecrypt/internal/config"
	"github.com/londek/ipadecrypt/internal/tui"
	"github.com/londek/ipadecrypt/internal/updater"
	"github.com/spf13/cobra"
)

func authHandler(cmd *cobra.Command, args []string) {
	cfg, paths, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	upd := updater.Start(context.Background(), Version, cfg)
	defer upd.Wait()

	if authReset {
		cfg.Apple = config.Apple{}
		if err := cfg.Save(); err != nil {
			tui.Err("reset auth: %v", err)
			return
		}
	}

	as, err := appstore.New(filepath.Join(paths.Root, "cookies"))
	if err != nil {
		tui.Err("appstore client: %v", err)
		return
	}

	account, err := authenticateApple(cfg, as)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	appStoreCountry, err := appstore.CountryCodeFromStoreFront(account.StoreFront)
	if err != nil {
		tui.Err("resolve appstore country code: %v", err)
		return
	}

	tui.Fields(
		"Apple ID", redact(account.Email),
		"Name", account.Name,
		"Storefront", fmt.Sprintf("%s (%s)", redact(account.StoreFront), redact(appStoreCountry)),
	)

	if err := cfg.Save(); err != nil {
		tui.Err("save config: %v", err)
		return
	}

	tui.OK("auth refreshed")
}

func authenticateApple(cfg *config.Config, as *appstore.Client) (*appstore.Account, error) {
	tui.Header("App Store Auth")
	tui.Info("Refreshes the saved Apple ID session used for App Store downloads.")

	if cfg.Apple.Email == "" {
		s, err := tui.Prompt("Apple ID email")
		if err != nil {
			return nil, err
		}

		cfg.Apple.Email = strings.TrimSpace(s)
	}

	if cfg.Apple.Password == "" {
		s, err := tui.PromptPassword("Apple ID password")
		if err != nil {
			return nil, err
		}

		cfg.Apple.Password = s
	}

	var (
		account  *appstore.Account
		authCode string
	)

	for attempt := 0; attempt < 3 && account == nil; attempt++ {
		live := tui.NewLive()
		live.Spin("authenticating")

		var err error
		account, err = as.Login(cfg.Apple.Email, cfg.Apple.Password, authCode)
		switch {
		case errors.Is(err, appstore.ErrAuthCodeRequired):
			live.Stop()

			code, err := tui.Prompt("Apple sent a 6-digit code - enter it")
			if err != nil {
				return nil, err
			}

			authCode = strings.TrimSpace(code)

		case err != nil:
			live.Stop()
			return nil, fmt.Errorf("login failed: %w", err)

		default:
			live.OK("authenticated")
		}
	}

	if account == nil {
		return nil, errors.New("login: 3 two-factor attempts failed")
	}

	cfg.Apple.SetAccount(account)
	return account, nil
}
