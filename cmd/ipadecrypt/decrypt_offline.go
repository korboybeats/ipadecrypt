package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/londek/ipadecrypt/internal/appstore"
	"github.com/londek/ipadecrypt/internal/fairplay"
	"github.com/londek/ipadecrypt/internal/pipeline"
	"github.com/londek/ipadecrypt/internal/tui"
	"github.com/londek/ipadecrypt/internal/updater"
	"github.com/spf13/cobra"
)

// offlineDecryptHandler is upstream's device-free decrypt flow: it downloads
// the encrypted IPA from the App Store (or reads a local .ipa) and decrypts it
// on this machine with the account's minted FairPlay credential. Nothing is
// installed on the device. Selected with `decrypt --offline`.
func offlineDecryptHandler(cmd *cobra.Command, args []string) {
	cfg, paths, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	upd := updater.Start(context.Background(), resolveVersion(), cfg)
	defer upd.Wait()

	target, err := parseDecryptArg(args[0])
	if err != nil {
		tui.Err("%v", err)
		return
	}

	dsid := cfg.Apple.DirectoryServicesIdentifier
	if dsid == "" {
		tui.Err("no Apple account configured")
		tui.Info("run `ipadecrypt bootstrap` to sign in, then `ipadecrypt mint` to mint your decryption credential")

		return
	}

	credential, err := loadCachedCredential(paths, dsid)
	if err != nil {
		tui.Err("no decryption credential for this account: %v", err)
		tui.Info("run `ipadecrypt mint` to mint it (one-time, needs your jailbroken device over root SSH)")

		return
	}

	cleanups := &cleanupStack{}
	defer cleanups.run()

	sigCh := make(chan os.Signal, 1)

	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		sig, ok := <-sigCh
		if !ok {
			return
		}

		signal.Reset(syscall.SIGINT, syscall.SIGTERM)
		tui.Warn("interrupted (%v), cleaning up (press again to force quit)", sig)
		cleanups.run()
		os.Exit(130)
	}()

	//
	// Acquire the encrypted IPA, either from the App Store or a local path.
	//

	var (
		appBundleID string
		appVersion  string
		encPath     string
	)

	if target.localPath != "" {
		tui.OK("local IPA %s", filepath.Base(target.localPath))

		appBundleID, appVersion, err = pipeline.AppInfo(target.localPath)
		if err != nil {
			tui.Err("read IPA: %v", err)
			return
		}

		encPath = target.localPath

		tui.OK("%s v%s", appBundleID, appVersion)
	} else {
		if cfg.Apple.Email == "" {
			tui.Err("this app must be downloaded from the App Store, but no Apple ID is configured")
			tui.Info("run `ipadecrypt bootstrap` to sign in, or decrypt a local .ipa instead")

			return
		}

		as, err := appstore.New(filepath.Join(paths.Root, "cookies"))
		if err != nil {
			tui.Err("appstore client: %v", err)
			return
		}

		acc, err := accountWithStorefront(cfg, decryptStorefront)
		if err != nil {
			tui.Err("storefront: %v", err)
			return
		}

		appStoreCountry, err := appstore.CountryCodeFromStoreFront(acc.StoreFront)
		if err != nil {
			tui.Err("resolve appstore country code: %v", err)
			return
		}

		tui.OK("signed in as %s (%s storefront)", redact(acc.Email), redact(appStoreCountry))

		// No device to fuzzy-match installed apps against, so a bare search
		// term (no dot) is resolved through App Store search instead.
		if target.bundleId != "" && !strings.Contains(target.bundleId, ".") {
			resolved, err := resolveBundleByAppStoreSearch(as, acc, target.bundleId)
			if err != nil {
				tui.Err("%v", err)
				return
			}

			target.bundleId = resolved
		}

		live := tui.NewLive()

		if target.appId != "" {
			live.Spin("resolving appId %s", target.appId)
		} else {
			live.Spin("resolving bundleId %s", target.bundleId)
		}

		app, err := lookupTargetApp(as, acc, target)
		if err != nil {
			live.Fail("lookup failed (%s): %v", appStoreCountry, err)
			return
		}

		live.OK("found %s on App Store", app.BundleID)

		live = tui.NewLive()
		live.Spin("fetching download metadata")

		disposition, err := fetchRemoteEncryptedSource(cfg, paths, as, app, decryptExtVerID, decryptStorefront, func(e authEvent) {
			switch e {
			case authReauth:
				live.Spin("re-authenticating")
			case authLicense:
				live.Spin("acquiring license")
			case authRetryingDownload:
				live.Spin("retrying download")
			}
		}, func(cur, total int64) {
			live.Message("%s", transferProgressText("downloading IPA from App Store", cur, total))
			live.Progress(cur, total)
		})
		if err != nil {
			if errors.Is(err, errRemoteDownloadFailed) {
				live.Fail("download failed: %v", errors.Unwrap(err))
				return
			}

			if reportAppStoreDownloadError(live, acc, err) {
				return
			}

			live.Fail("prepare failed: %v", err)

			return
		}

		appBundleID = app.BundleID
		appVersion = disposition.version
		encPath = disposition.path

		if disposition.kind == sourceDispositionCached {
			live.OK("cached %s", filepath.Base(encPath))
		} else {
			live.OK("downloaded %s", filepath.Base(encPath))
		}
	}

	//
	// Offline FairPlay decryption (no device).
	//

	outLocal, err := localOutputPath(decryptOutput, appBundleID, appVersion)
	if err != nil {
		tui.Err("output path: %v", err)
		return
	}

	if err := os.MkdirAll(filepath.Dir(outLocal), 0o755); err != nil {
		tui.Err("mkdir local: %v", err)
		return
	}

	outFile, err := os.OpenFile(outLocal, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		tui.Err("open local: %v", err)
		return
	}

	abandonLocal := true

	cleanups.push(func() {
		outFile.Close()

		if abandonLocal {
			os.Remove(outLocal)
		}
	})

	live := tui.NewLive()
	live.Spin("decrypting %s", filepath.Base(encPath))

	cw := &countingWriter{w: outFile, onTick: func(n int64) {
		live.Message("writing IPA → %s", humanBytes(n))
	}}

	summary, err := fairplay.Decrypt(encPath, cw, credential, decryptSkipAppex)
	if err != nil {
		live.Fail("decrypt failed: %v", err)

		if strings.Contains(err.Error(), "does not decode this SINF") {
			tui.Info("this credential belongs to a different Apple account; sign in with the account that owns the app and re-run `ipadecrypt mint --remint`")
		}

		return
	}

	if err := outFile.Sync(); err != nil {
		live.Fail("sync local: %v", err)
		return
	}

	if err := outFile.Close(); err != nil {
		live.Fail("close local: %v", err)
		return
	}

	live.OK("decrypted %d binary(s), %d page(s) (%s → %s)", summary.Binaries, summary.Pages, humanBytes(cw.n), outLocal)

	// Match the device flow's output hygiene: drop the Apple ID purchase
	// receipt and the Watch app unless asked to keep them.
	cleanup, err := pipeline.CleanupIPA(outLocal, pipeline.CleanupOptions{
		StripMetadata: !decryptKeepMetadata,
		StripWatch:    !decryptKeepWatch,
	})
	if err != nil {
		tui.Err("clean output IPA: %v", err)
		return
	}

	if cleanup.MetadataRemoved {
		tui.OK("stripped iTunesMetadata.plist")
	}

	if cleanup.WatchRemoved > 0 {
		tui.OK("stripped %d Watch/ entries", cleanup.WatchRemoved)
	}

	abandonLocal = false

	if decryptVerbose {
		tui.Info("output: %s", outLocal)
	}

	//
	// Verify.
	//

	if decryptNoVerify {
		return
	}

	compareSource := decryptExtraVerify

	src := ""
	if compareSource {
		src = encPath
	}

	live = tui.NewLive()
	if compareSource {
		live.Spin("verifying Mach-Os (cryptid, zero-fill, source byte-compare)")
	} else {
		live.Spin("verifying Mach-Os (cryptid, zero-fill)")
	}

	res, err := pipeline.Verify(outLocal, src, decryptSkipAppex)
	if err != nil {
		live.Fail("verify failed: %v", err)
		return
	}

	if !res.OK() {
		live.Fail("verify failed: %s", verifyFailureSummary(res))

		for _, n := range res.StillEncrypted {
			tui.Info("  %s still encrypted (cryptid != 0)", n)
		}

		for _, n := range res.AllZeroCrypt {
			tui.Info("  %s crypt region all zeros", n)
		}

		for _, m := range res.Mismatches {
			tui.Info("  %s %s", m.Name, m.Reason)
		}

		return
	}

	live.OK("%s", verifyOKSummary(res, compareSource))
}

// reportAppStoreDownloadError prints a tailored failure for the App Store
// download errors upstream surfaces (unowned paid app, attestation-only
// builds). It reports whether it handled err.
func reportAppStoreDownloadError(live *tui.Live, acc *appstore.Account, err error) bool {
	switch {
	case errors.Is(err, appstore.ErrPaidAppNotOwned):
		email := ""
		if acc != nil {
			email = acc.Email
		}

		live.Fail("paid app not owned by %s. sign in with the Apple ID that purchased it", redact(email))

		return true

	case errors.Is(err, appstore.ErrModernDownloadUnsupported):
		live.Fail("can't download this build from the App Store yet")
		tui.Info("Apple now requires device attestation for this app's current build; its download endpoints answer HTTP 403.")
		tui.Info("ipadecrypt doesn't implement that yet. Pick an older version, decrypt the installed app, or decrypt a local .ipa instead.")

		return true
	}

	return false
}
