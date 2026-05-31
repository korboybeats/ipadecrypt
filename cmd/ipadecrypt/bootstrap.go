package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/londek/ipadecrypt/internal/appstore"
	"github.com/londek/ipadecrypt/internal/config"
	"github.com/londek/ipadecrypt/internal/device"
	"github.com/londek/ipadecrypt/internal/tui"
	"github.com/londek/ipadecrypt/internal/updater"
	"github.com/spf13/cobra"
)

func bootstrapHandler(cmd *cobra.Command, args []string) {
	cfg, paths, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	upd := updater.Start(context.Background(), Version, cfg)
	defer upd.Wait()

	if bootstrapReset {
		cfg.Apple = config.Apple{}
		cfg.Device = config.Device{AcceptNewHostKey: true}
		if err := cfg.Save(); err != nil {
			tui.Err("reset config: %v", err)
			return
		}
	}

	// ---- Step 1: App Store sign-in -----------------------------------

	tui.Step(1, 5, "Sign in to the App Store")
	tui.Info("ipadecrypt requires an Apple ID to download .ipas.\nIt has to the be same Apple ID used on the jailbroken device.\nCredentials are stored locally on this machine.")

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

	// ---- Step 2: connect to device -----------------------------------

	tui.Step(2, 5, "Connect to the jailbroken device")
	tui.Info("ipadecrypt drives the iPhone over SSH. On the device install from Sileo:")
	tui.Bullet("OpenSSH   search \"OpenSSH\" in Sileo")
	tui.Info("Find the device IP in Settings → Wi-Fi.")
	tui.Info("Multiple IPs may be entered comma-separated; the first reachable one is used.")

	if cfg.Device.Host == "" {
		s, err := tui.Prompt("device IP/host")
		if err != nil {
			return
		}

		cfg.Device.Host = strings.TrimSpace(s)
	}

	if cfg.Device.Port == 0 {
		s, err := tui.PromptDefault("device SSH port", "22")
		if err != nil {
			return
		}
		p, perr := strconv.Atoi(strings.TrimSpace(s))
		if perr != nil || p < 1 || p > 65535 {
			tui.Err("invalid port: %s", s)
			return
		}
		cfg.Device.Port = p
	}

	if cfg.Device.User == "" {
		u, err := tui.PromptDefault("device SSH user", "mobile")
		if err != nil {
			return
		}

		cfg.Device.User = u
	}

	if cfg.Device.Auth.Kind == "" {
		idx, err := tui.Select("authentication method", []string{
			"password",
			"SSH public key",
		})
		if err != nil {
			return
		}

		if idx == 1 {
			cfg.Device.Auth.Kind = "key"
		} else {
			cfg.Device.Auth.Kind = "password"
		}
	}

	switch cfg.Device.Auth.Kind {
	case "key":
		if cfg.Device.Auth.KeyPath == "" {
			p, err := tui.PromptDefault("SSH private key path", "~/.ssh/id_ed25519")
			if err != nil {
				return
			}

			cfg.Device.Auth.KeyPath = strings.TrimSpace(p)
		}

		if cfg.Device.Auth.KeyPassphrase == "" {
			pass, err := tui.PromptPassword("key passphrase (leave empty if unencrypted)")
			if err != nil {
				return
			}

			cfg.Device.Auth.KeyPassphrase = pass
		}

		if cfg.Device.Auth.Password == "" && cfg.Device.User != "root" {
			pw, err := tui.PromptPassword("sudo password (leave empty if not needed)")
			if err != nil {
				return
			}

			cfg.Device.Auth.Password = pw
		}

	default:
		if cfg.Device.Auth.Password == "" {
			pw, err := tui.PromptPassword("device SSH password")
			if err != nil {
				return
			}

			cfg.Device.Auth.Password = pw
		}
	}

	var probe device.ProbeResult
	var connectedHost string
	connected := false

	func() {
		live := tui.NewLive()
		live.Spin("connecting to %s@%s", cfg.Device.User, cfg.Device.Host)

		dev, err := device.Connect(context.Background(), cfg.Device)
		if err != nil {
			live.Fail("ssh connect failed: %v", err)
			tui.Info("check that OpenSSH is running on the device and the password is correct")

			return
		}

		defer dev.Close()

		connectedHost = dev.Host()

		live.Spin("probing device")

		probe, err = dev.Probe()
		if err != nil {
			live.Fail("probe failed: %v", err)
			return
		}

		if err := cfg.Save(); err != nil {
			live.Fail("save config: %v", err)
			return
		}

		live.OK("connected")

		connected = true
	}()

	if !connected {
		return
	}

	tui.Fields(
		"Host", fmt.Sprintf("%s@%s", cfg.Device.User, connectedHost),
		"iOS", probe.IOSVersion,
		"Arch", probe.Arch,
		"Model", probe.Model,
		"Jailbreak", probe.Jailbreak,
	)

	// ---- Step 3: device prerequisites --------------------------------

	tui.Step(3, 5, "Install device prerequisites")
	tui.Info("ipadecrypt needs these packages on the jailbroken device:")
	tui.Bullet("AppSync Unified   bypasses installd's signature check")
	tui.Bullet("                  add repo: https://lukezgd.github.io/repo")
	tui.Bullet("appinst           installs modified IPAs on the device")
	tui.Info("A reboot may be needed after installing; that's fine, we'll reconnect.")

	prevLines := 0
	prompt := "press Enter once installed to verify"

	for {
		if prevLines > 0 {
			tui.Erase(prevLines)
		}

		// Fresh SSH connection each iteration so a reboot mid-bootstrap is safe.
		printed := 0
		missing := 0
		connectionFailed := false

		pdev, err := device.Connect(context.Background(), cfg.Device)
		if err != nil {
			tui.Err("ssh connect failed: %v", err)

			printed = 1
			connectionFailed = true
		} else {
			checks := []struct {
				name  string
				probe func() (string, error)
			}{
				{"AppSync Unified", pdev.LocateAppSync},
				{"appinst", pdev.LocateAppinst},
			}
			for _, c := range checks {
				p, err := c.probe()
				switch {
				case err != nil:
					tui.Err("%s - %v", c.name, err)

					missing++
				case p == "":
					tui.Err("%s - not found", c.name)

					missing++
				default:
					tui.OK("%s → %s", c.name, p)
				}

				printed++
			}

			pdev.Close()
		}

		if !connectionFailed && missing == 0 {
			break
		}

		if err := tui.PressEnter(prompt); err != nil {
			return
		}

		// Status rows + prompt row + Enter-echo row.
		prevLines = printed + 2

		if connectionFailed {
			prompt = "press Enter to retry"
		} else {
			prompt = fmt.Sprintf("%d missing - press Enter to retry", missing)
		}
	}

	// ---- Step 4: helper upload + verify ------------------------------

	tui.Step(4, 5, "Install the decrypt helper")
	tui.Info("A small embedded C binary that reads FairPlay-decrypted pages from a\nsuspended task. Uploaded once to /var/mobile/Documents/ipadecrypt/helpers/\nand cached by SHA thereafter.")

	live := tui.NewLive()
	live.Spin("connecting to %s@%s", cfg.Device.User, cfg.Device.Host)

	dev, err := device.Connect(context.Background(), cfg.Device)
	if err != nil {
		live.Fail("ssh connect failed: %v", err)
		return
	}

	defer dev.Close()

	live.Spin("uploading helper binary")

	helperPath, err := dev.EnsureHelper()
	if err != nil {
		live.Fail("upload failed: %v", err)
		return
	}

	live.Spin("verifying helper can exec")

	if err := dev.VerifyHelper(helperPath); err != nil {
		live.Fail("verify failed: %v", err)
		return
	}

	live.OK("helper ready at %s", helperPath)

	// ---- Step 5: auto-confirm tweak ----------------------------------

	tui.Step(5, 5, "Install the auto-confirm tweak")
	tui.Info("An optional SpringBoard tweak that taps the App Store older-version\nDownload prompt only while ipadecrypt's Latest iOS-compatible sentinel is armed.\nWithout it, you tap Download manually each time.")

	if dev.IsAutoalertInstalled() {
		tui.OK("ipadecryptautoalert already installed")
	} else {
		idx, err := tui.Select("install ipadecryptautoalert?", []string{
			"yes (installs the .deb and resprings SpringBoard)",
			"skip (you'll tap Download manually each time)",
		})
		if err != nil {
			return
		}
		if idx == 1 {
			tui.Info("skipped - you can re-run bootstrap any time to install it")
		} else {
			live = tui.NewLive()
			live.Spin("installing ipadecryptautoalert")
			if err := dev.EnsureAutoalert(); err != nil {
				live.Fail("install: %v", err)
				tui.Info("StoreKit downloads will still work but you'll have to tap 'Download' manually")
			} else {
				live.Spin("respringing to load tweak")
				if err := dev.Respring(); err != nil {
					live.Fail("respring: %v", err)
				} else {
					live.OK("installed and respringing")
				}
			}
		}
	}

	tui.Spacer()
	tui.OK("bootstrap complete - run `ipadecrypt decrypt <bundle-id|app-store-id|app-store-url|path-to-local-ipa>` to decrypt an app")
}
