package main

import (
	"context"
	"errors"
	"strings"

	"github.com/londek/ipadecrypt/internal/config"
	"github.com/londek/ipadecrypt/internal/credmint"
	"github.com/londek/ipadecrypt/internal/tui"
	"github.com/spf13/cobra"
)

// mintHandler runs upstream's one-time credential mint against the device
// configured by bootstrap. The credential enables `decrypt --offline`.
func mintHandler(cmd *cobra.Command, args []string) {
	cfg, paths, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	dsid := cfg.Apple.DirectoryServicesIdentifier
	if dsid == "" {
		tui.Err("no Apple account configured")
		tui.Info("run `ipadecrypt bootstrap` (without --skip-login) first; the credential is tied to that Apple ID")

		return
	}

	if credentialCached(paths, dsid) && !mintRemint {
		tui.OK("credential already cached for this account")
		tui.Info("pass --remint to replace it")

		return
	}

	if cfg.Device.Host == "" && !cfg.Device.USB {
		tui.Err("no device configured")
		tui.Info("run `ipadecrypt bootstrap` first")

		return
	}

	tui.Info("This connects once to your jailbroken device to extract a reusable\n16-byte account key. After this, `decrypt --offline` runs without the device.")
	tui.Info("Requirements on the device:")
	tui.Bullet("root SSH (OpenSSH from Sileo)")
	tui.Bullet("at least one App Store app bought with this Apple ID, installed")

	dev, err := mintDevice(cfg.Device)
	if err != nil {
		return
	}

	live := tui.NewLive()
	live.Spin("minting credential (one-time)")

	credential, err := credmint.Mint(context.Background(), dev, dsid)
	if err != nil {
		if errors.Is(err, credmint.ErrBinariesUnavailable) {
			live.Fail("this build cannot mint credentials")
			tui.Info("the device binaries aren't compiled into this build; install an official release")

			return
		}

		live.Fail("mint failed: %v", err)

		return
	}

	if err := saveCredential(paths, dsid, credential); err != nil {
		live.Fail("cache credential: %v", err)
		return
	}

	live.OK("credential minted and cached")
	tui.Info("run `ipadecrypt decrypt --offline <bundle-id|app-store-id|app-store-url|path-to-local-ipa>`")
}

// mintDevice derives the mint connection from the bootstrap device config.
// The device flow supports comma-separated hosts and a mobile user; minting
// needs a single host and root, so the first host is used and root
// credentials are prompted for (and not saved) when the saved user isn't root.
// A non-nil error only signals a user abort.
func mintDevice(saved config.Device) (config.Device, error) {
	dev := saved

	if !dev.USB {
		dev.Host = strings.TrimSpace(strings.Split(dev.Host, ",")[0])
	}

	if dev.Port == 0 {
		dev.Port = 22
	}

	if dev.User == "root" {
		return dev, nil
	}

	tui.Warn("the saved SSH user is %q; minting needs root", dev.User)

	dev.User = "root"

	if dev.Auth.Kind == "password" {
		pass, err := tui.PromptPassword("root SSH password (not saved)")
		if err != nil {
			return dev, err
		}

		dev.Auth.Password = pass
	}

	return dev, nil
}
