package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/londek/ipadecrypt/internal/appstore"
	"github.com/londek/ipadecrypt/internal/config"
	"github.com/londek/ipadecrypt/internal/device"
	"github.com/londek/ipadecrypt/internal/pipeline"
	"github.com/londek/ipadecrypt/internal/tui"
	"github.com/londek/ipadecrypt/internal/updater"
	"github.com/spf13/cobra"
)

var (
	appStoreIdRegex = regexp.MustCompile(`/id(\d+)`)

	errAppinstNotFound = errors.New("appinst not found")
)

type decryptTarget struct {
	localPath string
	bundleId  string
	appId     string
}

type patchResult struct {
	uploadPath           string
	patchedPath          string
	changed              bool
	previousMinOS        string
	watchStripped        int
	deviceFamilyExpanded bool
	previousDeviceFamily []int
	newDeviceFamily      []int
}

type installPlan struct {
	helperPath          string
	appinstPath         string
	bundleID            string
	bundlePath          string
	stagingRemote       string
	stagingUploadRemote string
}

type installResult struct {
	bundlePath      string
	installed       bool
	reinstalled     bool
	previousVersion string
}

type sourceDisposition byte

const (
	sourceDispositionCached sourceDisposition = iota + 1
	sourceDispositionDownloaded
)

type installEvent int

const (
	installHashIPA installEvent = iota + 1
	installHashInstalled
	installReadInstalledVersion
	installReplaceInstalled
	installUpload
	installRunAppinst
	installRescan
)

type helperUpdate struct {
	spin         string
	note         string
	progress     bool
	progressCur  int64
	progressMax  int64
	progressText string
}

type helperProgress struct {
	dumpedTotal      atomic.Int64
	dumpedMain       atomic.Int64
	dumpedFrameworks atomic.Int64
	dumpedOther      atomic.Int64
}

func parseDecryptArg(raw string) (decryptTarget, error) {
	// App Store URL
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil {
			return decryptTarget{}, fmt.Errorf("parse url: %w", err)
		}

		m := appStoreIdRegex.FindStringSubmatch(u.Path)
		if m == nil {
			return decryptTarget{}, fmt.Errorf("no /id<digits> in url %s", raw)
		}

		return decryptTarget{appId: m[1]}, nil
	}

	// Local .ipa path
	if strings.HasSuffix(strings.ToLower(raw), ".ipa") {
		info, err := os.Stat(raw)
		if err != nil {
			return decryptTarget{}, fmt.Errorf("local IPA %s: %w", raw, err)
		}

		if info.IsDir() {
			return decryptTarget{}, fmt.Errorf("local IPA %s is a directory", raw)
		}

		abs, err := filepath.Abs(raw)
		if err != nil {
			return decryptTarget{}, err
		}

		return decryptTarget{localPath: abs}, nil
	}

	// Bare numeric string: App Store track ID (e.g. "544007664").
	if isAllDigits(raw) {
		return decryptTarget{appId: raw}, nil
	}

	// Fallback: treat as a bundle identifier.
	return decryptTarget{bundleId: raw}, nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func transferProgressText(label string, cur, total int64) string {
	if total <= 0 {
		return label
	}

	return fmt.Sprintf("%s (%s / %s)", label, humanBytes(cur), humanBytes(total))
}

func humanBytes(n int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case n >= TB:
		return fmt.Sprintf("%.2f TB", float64(n)/float64(TB))
	case n >= GB:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func decryptHandler(cmd *cobra.Command, args []string) {
	if decryptFromAppStore && decryptUseInstalled {
		tui.Err("--from-appstore and --use-installed are mutually exclusive; pass at most one.")
		return
	}

	if decryptForceUninstall && decryptNoUninstall {
		tui.Err("--force-uninstall and --no-uninstall are mutually exclusive; pass at most one.")
		return
	}

	cfg, paths, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	keepPolicy, err := effectiveDecryptKeepPolicy(cfg)
	if err != nil {
		tui.Err("%v", err)
		return
	}
	if decryptOutput != "" && keepPolicy == config.OutputKeepDevice {
		tui.Err("--output requires --keep desktop or --keep both")
		return
	}

	upd := updater.Start(context.Background(), Version, cfg)
	defer upd.Wait()

	target, err := parseDecryptArg(args[0])
	if err != nil {
		tui.Err("%v", err)
		return
	}

	if cfg.Apple.Email == "" || cfg.Device.Host == "" {
		tui.Err("environment not configured")
		tui.Info("run `ipadecrypt bootstrap` first to prepare your environment")

		return
	}

	//
	// Connect to device and probe environment
	//

	live := tui.NewLive()
	live.Spin("connecting to %s@%s", cfg.Device.User, cfg.Device.Host)

	dev, err := device.Connect(context.Background(), cfg.Device)
	if err != nil {
		live.Fail("ssh connect failed: %v", err)
		return
	}

	cleanups := &cleanupStack{}
	defer cleanups.run()

	// dev.Close pushed first so it runs LAST: remote rm/uninstall need a
	// live SSH session.
	cleanups.push(dev.Close)

	sigCh := make(chan os.Signal, 1)

	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		sig, ok := <-sigCh
		if !ok {
			return
		}

		// Second Ctrl-C should hard-kill in case cleanup hangs.
		signal.Reset(syscall.SIGINT, syscall.SIGTERM)
		tui.Warn("interrupted (%v), cleaning up (press again to force quit)", sig)
		cleanups.run()
		os.Exit(130)
	}()

	var (
		uninstall           bool
		uninstallBundleID   string
		uninstallBundlePath string
	)

	cleanups.push(func() {
		if !uninstall || uninstallBundlePath == "" {
			return
		}

		if err := dev.Uninstall(uninstallBundlePath); err != nil {
			tui.Err("uninstall %s: %v", uninstallBundleID, err)
			return
		}

		tui.OK("uninstalled %s", uninstallBundleID)
	})

	live.Spin("probing device")

	probe, err := dev.Probe()
	if err != nil {
		live.Fail("probe failed: %v", err)
		return
	}

	live.OK("ipadecrypt %s · %s@%s iOS %s %s %s (%s)", Version, cfg.Device.User, dev.Host(), probe.IOSVersion, probe.Arch, probe.Model, probe.Jailbreak)

	//
	// Fuzzy resolution: if target looks like a search term (no dot), match
	// against installed apps' bundle IDs and display names. Pick a unique
	// match automatically; prompt on ambiguity.
	//

	if target.bundleId != "" && !strings.Contains(target.bundleId, ".") {
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
		resolved, err := resolveBundleByFuzzy(dev, as, acc, target.bundleId)
		if err != nil {
			tui.Err("%v", err)
			return
		}
		target.bundleId = resolved
	}

	selectedExtVerID := decryptExtVerID

	if target.bundleId != "" && !decryptFromAppStore {
		live = tui.NewLive()
		live.Spin("checking if %s is installed", target.bundleId)

		installedPath, canonicalID, err := dev.FindInstalledByBundleID(target.bundleId)
		if err != nil {
			live.Fail("scan failed: %v", err)
			return
		}

		if installedPath != "" {
			target.bundleId = canonicalID

			version, err := dev.InstalledVersion(installedPath)
			if err != nil || version == "" {
				version = "unknown"
			}

			live.OK("found installed %s v%s", target.bundleId, version)

			useInstalled := decryptUseInstalled
			useStoreKit := false
			useSelectedVersion := false
			if !useInstalled {
				if !tui.IsTTY() {
					tui.Err("%s v%s is already installed on the device.", target.bundleId, version)
					tui.Info("pass --use-installed to decrypt the installed build, --from-appstore to fetch fresh and reinstall, or run in a TTY.")

					return
				}

				idx, err := tui.Select(
					fmt.Sprintf("%s v%s is already installed - which build do you want decrypted?", target.bundleId, version),
					[]string{
						fmt.Sprintf("Installed build v%s", version),
						"Latest from App Store",
						"Latest iOS-compatible version",
						"Select App Store version",
					},
				)
				if err != nil {
					tui.Err("%v", err)
					return
				}

				switch idx {
				case 0:
					useInstalled = true
				case 2:
					useStoreKit = true
				case 3:
					useSelectedVersion = true
				}
			}

			if useInstalled {
				live = tui.NewLive()
				live.Spin("preparing helper")

				helperPath, err := dev.EnsureHelper(probe.Jailbreak)
				if err != nil {
					live.Fail("helper upload: %v", err)
					return
				}

				live.OK("helper ready")

				uninstall = decideUninstall(false, decryptForceUninstall, decryptNoUninstall)
				uninstallBundleID = target.bundleId
				uninstallBundlePath = installedPath

				runDecryptOnBundle(dev, cleanups, helperPath, target.bundleId, installedPath, version, "", keepPolicy, probe.Jailbreak)

				return
			}

			if useStoreKit {
				if ok := runStoreKitInstall(dev, target.bundleId, version, probe.Jailbreak); !ok {
					return
				}
				// re-resolve installed path/version after the install
				installedPath, _, _ = dev.FindInstalledByBundleID(target.bundleId)
				if installedPath == "" {
					tui.Err("%s no longer installed after StoreKit download", target.bundleId)
					return
				}
				newVersion, _ := dev.InstalledVersion(installedPath)
				if newVersion == "" {
					newVersion = "unknown"
				}

				live = tui.NewLive()
				live.Spin("preparing helper")

				helperPath, err := dev.EnsureHelper(probe.Jailbreak)
				if err != nil {
					live.Fail("helper upload: %v", err)
					return
				}

				live.OK("helper ready")

				runDecryptOnBundle(dev, cleanups, helperPath, target.bundleId, installedPath, newVersion, "", keepPolicy, probe.Jailbreak)

				return
			}

			if useSelectedVersion {
				extVerID, ok := selectAppStoreVersionForDecrypt(cfg, paths, target)
				if !ok {
					return
				}

				selectedExtVerID = extVerID
			}
			// idx == 1 falls through to the existing App Store reinstall path
		} else {
			live.OK("%s not installed", target.bundleId)

			if tui.IsTTY() {
				idx, err := tui.Select(
					fmt.Sprintf("which build of %s do you want decrypted?", target.bundleId),
					[]string{
						"Latest from App Store",
						"Latest iOS-compatible version",
						"Select App Store version",
					},
				)
				if err != nil {
					tui.Err("%v", err)
					return
				}

				switch idx {
				case 1:
					if ok := runStoreKitInstall(dev, target.bundleId, "", probe.Jailbreak); !ok {
						return
					}
					installedPath, _, _ := dev.FindInstalledByBundleID(target.bundleId)
					if installedPath == "" {
						tui.Err("%s not found after StoreKit download", target.bundleId)
						return
					}
					version, _ := dev.InstalledVersion(installedPath)
					if version == "" {
						version = "unknown"
					}

					live = tui.NewLive()
					live.Spin("preparing helper")

					helperPath, err := dev.EnsureHelper(probe.Jailbreak)
					if err != nil {
						live.Fail("helper upload: %v", err)
						return
					}

					live.OK("helper ready")

					runDecryptOnBundle(dev, cleanups, helperPath, target.bundleId, installedPath, version, "", keepPolicy, probe.Jailbreak)
					return
				case 2:
					extVerID, ok := selectAppStoreVersionForDecrypt(cfg, paths, target)
					if !ok {
						return
					}

					selectedExtVerID = extVerID
				}
			}
		}
	}

	//
	// Acquire encrypted IPA, either from the App Store or a local path
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

		live = tui.NewLive()

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

		if app.Price > 0 {
			live.Fail("paid app (price=%v) - unsupported", app.Price)
			return
		}

		live.OK("found %s on App Store", app.BundleID)

		live = tui.NewLive()
		live.Spin("fetching download metadata")

		disposition, err := fetchRemoteEncryptedSource(cfg, paths, as, app, selectedExtVerID, decryptStorefront, func(e authEvent) {
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
	// Patching MinimumOSVersion if needed
	//

	live = tui.NewLive()
	live.Spin("patching Info.plist %s", probe.IOSVersion)

	patch, err := patchSourceForDevice(encPath, probe.IOSVersion, probe.DeviceFamily, decryptPatchDevType)
	if err != nil {
		var dfErr *pipeline.ErrDeviceFamilyMismatch
		if errors.As(err, &dfErr) {
			live.Fail("device family mismatch: app supports %v, device is %d (%s) - pass --patch-device-type to install anyway",
				dfErr.Supported, dfErr.Device, pipeline.DeviceFamilyName(dfErr.Device))

			return
		}

		live.Fail("patch Info.plist failed: %v", err)

		return
	}

	cleanups.push(func() {
		if patch.patchedPath != "" {
			os.Remove(patch.patchedPath)
		}
	})

	live.OK("patched Info.plist")

	if patch.changed {
		tui.OK("MinimumOSVersion %s → %s", patch.previousMinOS, probe.IOSVersion)
	}

	if patch.deviceFamilyExpanded {
		tui.OK("UIDeviceFamily %v → %v", patch.previousDeviceFamily, patch.newDeviceFamily)
	}

	if patch.watchStripped > 0 {
		tui.OK("stripped %d Watch/ entries", patch.watchStripped)
	}

	live = tui.NewLive()
	live.Spin("preparing install plan")

	plan, err := buildInstallPlan(dev, patch.uploadPath, appBundleID, probe.Jailbreak)
	if err != nil {
		switch {
		case errors.Is(err, errAppinstNotFound):
			live.Fail("appinst not found on device - run `ipadecrypt bootstrap`")
		default:
			live.Fail("prepare install: %v", err)
		}

		return
	}

	cleanups.push(func() {
		if plan.stagingRemote != "" && !decryptNoCleanup {
			dev.Remove(plan.stagingRemote)
		}
		if plan.stagingUploadRemote != "" && plan.stagingUploadRemote != plan.stagingRemote && !decryptNoCleanup {
			dev.Remove(plan.stagingUploadRemote)
		}
	})

	if plan.bundlePath == "" {
		live.Spin("preparing install")
	} else {
		live.Spin("checking installed app at %s", plan.bundlePath)
	}

	install, err := ensureInstalledBundle(dev, plan, patch.uploadPath, func(e installEvent) {
		switch e {
		case installHashIPA:
			live.Spin("computing IPA checksum")
		case installHashInstalled:
			live.Spin("computing installed app checksum")
		case installReadInstalledVersion:
			live.Spin("reading installed app version")
		case installReplaceInstalled:
			live.Spin("installed app differs - replacing it")
		case installUpload:
			live.Spin("uploading IPA to device")
		case installRunAppinst:
			live.Spin("running appinst")
		case installRescan:
			live.Spin("locating installed app")
		}
	}, func(cur, total int64) {
		live.Message("%s", transferProgressText("uploading IPA to device", cur, total))
		live.Progress(cur, total)
	})
	if err != nil {
		live.Fail("install failed: %v", err)
		return
	}

	if install.reinstalled {
		live.OK("reinstalled (%s => %s) → %s", install.previousVersion, appVersion, install.bundlePath)
	} else if install.installed {
		live.OK("installed → %s", install.bundlePath)
	} else {
		live.OK("already installed → %s", install.bundlePath)
	}

	uninstall = decideUninstall(install.installed || install.reinstalled, decryptForceUninstall, decryptNoUninstall)
	uninstallBundleID = appBundleID
	uninstallBundlePath = install.bundlePath

	runDecryptOnBundle(dev, cleanups, plan.helperPath, appBundleID, install.bundlePath, appVersion, encPath, keepPolicy, probe.Jailbreak)
}

// decideUninstall picks the post-decrypt cleanup behavior. weInstalledIt
// is true when this run put the bundle on the device (fresh install or
// reinstall over a different version). force/no come from the flags.
func decideUninstall(weInstalledIt, force, no bool) bool {
	switch {
	case force:
		return true
	case no:
		return false
	}

	return weInstalledIt
}

func verifyOKSummary(res pipeline.VerifyResult, compareSource bool) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%d Mach-O(s) verified", res.Scanned)

	if compareSource {
		fmt.Fprintf(&b, ", %d source-matched", res.Compared)
	}

	var extras []string
	if len(res.Missing) > 0 {
		extras = append(extras, fmt.Sprintf("%d source-missing", len(res.Missing)))
	}

	if len(res.Skipped) > 0 {
		extras = append(extras, fmt.Sprintf("%d skipped", len(res.Skipped)))
	}

	if len(extras) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(extras, ", "))
	}

	return b.String()
}

func verifyFailureSummary(res pipeline.VerifyResult) string {
	parts := make([]string, 0, 3)

	if n := len(res.StillEncrypted); n > 0 {
		parts = append(parts, fmt.Sprintf("%d still encrypted", n))
	}

	if n := len(res.AllZeroCrypt); n > 0 {
		parts = append(parts, fmt.Sprintf("%d all-zero crypt", n))
	}

	if n := len(res.Mismatches); n > 0 {
		parts = append(parts, fmt.Sprintf("%d source diff", n))
	}

	return strings.Join(parts, ", ")
}

func shouldAbandonLocalOutput(completed bool) bool {
	return !completed
}

func pushLocalOutputCleanup(cleanups *cleanupStack, outFile io.Closer, outLocal string, cleanupTemp func(), completed, delivered *bool) {
	cleanups.push(func() {
		outFile.Close()

		if shouldAbandonLocalOutput(*completed) {
			os.Remove(outLocal)
			return
		}

		if cleanupTemp != nil && *delivered {
			cleanupTemp()
		}
	})
}

// runDecryptOnBundle writes the decrypted IPA locally. When srcIPAPath is
// present, the helper streams only decrypted Mach-Os and the host assembles
// the IPA from the original source. For use-installed/StoreKit paths, the
// helper streams a full IPA from the device.
func runDecryptOnBundle(dev *device.Client, cleanups *cleanupStack, helperPath, bundleID, bundlePath, version, srcIPAPath, keepPolicy, jailbreak string) {
	outRemote := remoteOutputPath(bundleID, version, jailbreak)

	keepDesktop := keepPolicy == config.OutputKeepDesktop || keepPolicy == config.OutputKeepBoth
	keepDevice := keepPolicy == config.OutputKeepDevice || keepPolicy == config.OutputKeepBoth

	outLocal, cleanupLocal, err := decryptWorkingOutputPath(keepDesktop, bundleID, version)
	if err != nil {
		tui.Err("output path: %v", err)
		return
	}

	if keepDevice {
		if err := dev.Mkdir(path.Dir(outRemote)); err != nil {
			tui.Err("mkdir device output: %v", err)
			return
		}
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

	// Drop only a partial stream. Once the IPA has been completely written and
	// synced, retain it even if cleanup or verification later reports a failure.
	localOutputComplete := false
	localOutputDelivered := false
	pushLocalOutputCleanup(cleanups, outFile, outLocal, cleanupLocal, &localOutputComplete, &localOutputDelivered)

	live := tui.NewLive()
	live.Spin("starting helper")

	progress := &helperProgress{}
	onEvent := func(ev device.Event) {
		update := progress.HandleEvent(ev)

		if update.note != "" {
			live.Note("%s", update.note)
		}

		if update.spin != "" {
			live.Spin("%s", update.spin)
		}

		if update.progress {
			if update.progressText != "" {
				live.Message("%s", update.progressText)
			}

			live.Progress(update.progressCur, update.progressMax)
		}
	}

	cw := &countingWriter{w: outFile, onTick: func(n int64) {
		live.Message("writing IPA → %s", humanBytes(n))
	}}

	if srcIPAPath != "" {
		// Host-side assembly: helper streams decrypted Mach-Os on stdout,
		// host fuses them with the (unpatched) source IPA into outFile.
		// No zip on device; output uses the original Info.plist as-is.
		assembleErr := pipeline.Assemble(srcIPAPath, cw, func(write pipeline.SubstituteWriter) error {
			code, err := dev.RunHelperExecs(helperPath, bundleID, bundlePath, decryptVerbose, decryptSkipAppex, onEvent, func(name string, _ int64, r io.Reader) error {
				return write(name, r)
			})
			if err != nil {
				return fmt.Errorf("helper run: %w", err)
			}

			if code != 0 {
				return fmt.Errorf("helper exit %d", code)
			}

			return nil
		})
		if assembleErr != nil {
			live.Fail("assemble: %v", assembleErr)
			return
		}
	} else {
		// Use-installed path: no source IPA on host, helper packages the
		// IPA on device and streams its bytes straight into outFile.
		code, err := dev.RunHelper(helperPath, bundleID, bundlePath, decryptVerbose, decryptSkipAppex, onEvent, cw)
		if err != nil {
			live.Fail("helper run: %v", err)
			return
		}

		if code != 0 {
			live.Fail("helper exit %d", code)
			return
		}
	}

	if err := outFile.Sync(); err != nil {
		live.Fail("sync local: %v", err)
		return
	}
	localOutputComplete = true

	live.OK("%s (%s → %s)", progress.Summary(), humanBytes(cw.n), outLocal)

	if !decryptKeepMetadata || !decryptKeepWatch {
		live.Spin("cleaning IPA")

		cleanup, err := pipeline.CleanupIPA(outLocal, pipeline.CleanupOptions{
			StripMetadata: !decryptKeepMetadata,
			StripWatch:    !decryptKeepWatch,
			Debug: func(msg string) {
				live.Note("%s", msg)
			},
		})
		if err != nil {
			live.Fail("IPA cleanup failed: %v", err)
			return
		}

		live.Note("cleanup: rewritten=%t", cleanup.Rewritten)

		if cleanup.MetadataRemoved || cleanup.WatchRemoved > 0 {
			parts := make([]string, 0, 2)
			if cleanup.MetadataRemoved {
				parts = append(parts, "iTunesMetadata.plist")
			}
			if cleanup.WatchRemoved > 0 {
				entryWord := "entry"
				if cleanup.WatchRemoved != 1 {
					entryWord = "entries"
				}
				parts = append(parts, fmt.Sprintf("%d Watch/ %s", cleanup.WatchRemoved, entryWord))
			}
			live.Note("stripped %s", strings.Join(parts, ", "))
		}
	}

	if keepDesktop {
		live.OK("→ %s", outLocal)
	}

	if !decryptNoVerify {
		if decryptExtraVerify && srcIPAPath == "" {
			tui.Info("extra-verify unavailable when source ipa is not present")
		}

		compareSource := decryptExtraVerify && srcIPAPath != ""

		src := ""
		if compareSource {
			src = srcIPAPath
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
		live.Note("verify: scanned=%d encrypted=%d skipped=%d", res.Scanned, len(res.StillEncrypted), len(res.Skipped))

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

	if keepDevice {
		live = tui.NewLive()
		live.Spin("syncing device copy")

		f, err := os.Open(outLocal)
		if err != nil {
			live.Fail("open final IPA: %v", err)
			return
		}

		if err := dev.Upload(f, outRemote, 0o644); err != nil {
			f.Close()
			live.Fail("sync device copy: %v", err)
			return
		}
		if err := f.Close(); err != nil {
			live.Fail("close final IPA: %v", err)
			return
		}
		localOutputDelivered = true

		live.OK("device copy ready")
	}

	if keepDesktop {
		tui.OK("kept on desktop → %s", outLocal)
	}
	if keepDevice {
		tui.OK("kept on device → %s", outRemote)
	}
}

func lookupTargetApp(as *appstore.Client, acc *appstore.Account, target decryptTarget) (appstore.App, error) {
	if target.appId != "" {
		return as.LookupByAppID(acc, target.appId)
	}

	return as.LookupByBundleID(acc, target.bundleId)
}

func selectAppStoreVersionForDecrypt(cfg *config.Config, paths *config.Paths, target decryptTarget) (string, bool) {
	if !tui.IsTTY() {
		tui.Err("Select App Store version requires a terminal")
		return "", false
	}

	if err := ensureVersionsWarningAccepted(cfg); err != nil {
		if err.Error() != "aborted" {
			tui.Err("%v", err)
		}

		return "", false
	}

	as, err := appstore.New(filepath.Join(paths.Root, "cookies"))
	if err != nil {
		tui.Err("appstore client: %v", err)
		return "", false
	}
	acc, err := accountWithStorefront(cfg, decryptStorefront)
	if err != nil {
		tui.Err("storefront: %v", err)
		return "", false
	}

	live := tui.NewLive()
	if target.appId != "" {
		live.Spin("resolving appId %s", target.appId)
	} else {
		live.Spin("resolving bundleId %s", target.bundleId)
	}

	app, err := lookupTargetApp(as, acc, target)
	if err != nil {
		live.Fail("lookup failed: %v", err)
		return "", false
	}

	if app.Price > 0 {
		live.Fail("paid app (price=%v) - unsupported", app.Price)
		return "", false
	}

	live.OK("found %s on App Store", app.BundleID)

	live = tui.NewLive()
	live.Spin("listing versions for %s", app.BundleID)

	list, err := listVersionsWithAuth(cfg, as, app, decryptStorefront)
	if err != nil {
		live.Fail("list versions failed: %v", err)
		return "", false
	}

	live.OK("%d version(s), latest %s", len(list.ExternalVersionIDs), list.LatestExternalVersionID)

	cache, cachePath, err := loadOrInitVersionsCache(paths, app.BundleID)
	if err != nil {
		tui.Err("cache path: %v", err)
		return "", false
	}

	extVerID, err := runSingleVersionPicker(cfg, as, app, list, cache, cachePath, "", decryptStorefront)
	if err != nil {
		if !errors.Is(err, errVersionSelectionAborted) {
			tui.Err("%v", err)
		}

		return "", false
	}

	return extVerID, true
}

var errRemoteDownloadFailed = errors.New("remote download failed")

type remoteSourceDisposition struct {
	path    string
	version string
	kind    sourceDisposition
}

func fetchRemoteEncryptedSource(cfg *config.Config, paths *config.Paths, as *appstore.Client, app appstore.App, extVerID, storefront string, onAuth func(authEvent), onProgress func(cur, total int64)) (remoteSourceDisposition, error) {
	if extVerID == "" {
		encPath, err := paths.CachedEncryptedIPA(app.BundleID, app.Version)
		if err != nil {
			return remoteSourceDisposition{}, err
		}

		if fileExists(encPath) {
			return remoteSourceDisposition{
				path:    encPath,
				version: app.Version,
				kind:    sourceDispositionCached,
			}, nil
		}
	}

	ticket, err := withAuth(cfg, as, app, storefront, 3, onAuth, func(acc *appstore.Account) (appstore.DownloadTicket, error) {
		return as.PrepareDownload(acc, app, extVerID)
	})
	if err != nil {
		return remoteSourceDisposition{}, err
	}

	encPath, err := paths.CachedEncryptedIPA(app.BundleID, ticket.Version())
	if err != nil {
		return remoteSourceDisposition{}, err
	}

	if fileExists(encPath) {
		return remoteSourceDisposition{
			path:    encPath,
			version: ticket.Version(),
			kind:    sourceDispositionCached,
		}, nil
	}

	_, err = withStorefrontAccount(cfg, storefront, func(acc *appstore.Account) (appstore.DownloadOutput, error) {
		return as.CompleteDownload(acc, ticket, encPath, onProgress)
	})
	if err != nil {
		return remoteSourceDisposition{}, fmt.Errorf("%w: %w", errRemoteDownloadFailed, err)
	}

	return remoteSourceDisposition{
		path:    encPath,
		version: ticket.Version(),
		kind:    sourceDispositionDownloaded,
	}, nil
}

func patchSourceForDevice(encPath, iosVersion string, deviceFamily int, patchDeviceType bool) (patchResult, error) {
	pattern := strings.TrimSuffix(filepath.Base(encPath), ".ipa") + "-patched-*.ipa"

	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return patchResult{}, fmt.Errorf("create temp ipa: %w", err)
	}

	tmp := f.Name()
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return patchResult{}, fmt.Errorf("close temp ipa: %w", err)
	}

	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return patchResult{}, fmt.Errorf("prepare temp ipa: %w", err)
	}

	res, err := pipeline.PatchForInstall(encPath, tmp, iosVersion, deviceFamily, patchDeviceType, decryptKeepWatch)
	if err != nil {
		os.Remove(tmp)
		return patchResult{}, err
	}

	if !res.MinOSChanged && res.WatchRemoved == 0 && !res.DeviceFamilyExpanded {
		os.Remove(tmp)
		return patchResult{uploadPath: encPath}, nil
	}

	return patchResult{
		uploadPath:           tmp,
		patchedPath:          tmp,
		changed:              res.MinOSChanged,
		previousMinOS:        res.PreviousMinOS,
		watchStripped:        res.WatchRemoved,
		deviceFamilyExpanded: res.DeviceFamilyExpanded,
		previousDeviceFamily: res.PreviousDeviceFamily,
		newDeviceFamily:      res.NewDeviceFamily,
	}, nil
}

func buildInstallPlan(dev *device.Client, uploadPath, bundleID, jailbreak string) (installPlan, error) {
	helperPath, err := dev.EnsureHelper(jailbreak)
	if err != nil {
		return installPlan{}, fmt.Errorf("helper upload: %w", err)
	}

	appinstPath, err := dev.LocateAppinst()
	if err != nil {
		return installPlan{}, fmt.Errorf("locate appinst: %w", err)
	}

	if appinstPath == "" {
		return installPlan{}, errAppinstNotFound
	}

	bundlePath, _, err := dev.FindInstalledByBundleID(bundleID)
	if err != nil {
		return installPlan{}, fmt.Errorf("scan installed: %w", err)
	}

	stagingRemote := path.Join(device.RemoteRoot, "staging", filepath.Base(uploadPath))

	return installPlan{
		helperPath:          helperPath,
		appinstPath:         appinstPath,
		bundleID:            bundleID,
		bundlePath:          bundlePath,
		stagingRemote:       stagingRemote,
		stagingUploadRemote: device.ResolveRemotePath(jailbreak, stagingRemote),
	}, nil
}

func ensureInstalledBundle(dev *device.Client, plan installPlan, uploadPath string, onEvent func(installEvent), onProgress func(cur, total int64)) (installResult, error) {
	notify := func(e installEvent) {
		if onEvent != nil {
			onEvent(e)
		}
	}

	if plan.bundlePath == "" {
		return installUploadedBundle(dev, plan, uploadPath, false, "", notify, onProgress)
	}

	if !decryptFromAppStore {
		notify(installHashIPA)

		execName, wantSum, err := pipeline.MainExecSHA256(uploadPath)
		if err != nil {
			return installResult{}, fmt.Errorf("hash ipa: %w", err)
		}

		remoteExec := path.Join(plan.bundlePath, execName)

		notify(installHashInstalled)

		gotSum, err := dev.HashFile(remoteExec)
		if err != nil {
			return installResult{}, fmt.Errorf("hash device: %w", err)
		}

		if gotSum == wantSum {
			return installResult{
				bundlePath: plan.bundlePath,
			}, nil
		}
	}

	notify(installReadInstalledVersion)

	previousVersion, err := dev.InstalledVersion(plan.bundlePath)
	if err != nil {
		previousVersion = ""
	}

	notify(installReplaceInstalled)

	return installUploadedBundle(dev, plan, uploadPath, true, previousVersion, notify, onProgress)
}

func installUploadedBundle(dev *device.Client, plan installPlan, uploadPath string, reinstalled bool, previousVersion string, notify func(installEvent), onProgress func(cur, total int64)) (installResult, error) {
	notify(installUpload)

	src, err := os.Open(uploadPath)
	if err != nil {
		return installResult{}, fmt.Errorf("open %s: %w", uploadPath, err)
	}

	defer src.Close()

	st, err := src.Stat()
	if err != nil {
		return installResult{}, fmt.Errorf("stat %s: %w", uploadPath, err)
	}

	pr := newProgressReader(src, st.Size(), onProgress)
	uploadRemote := plan.stagingUploadRemote
	if uploadRemote == "" {
		uploadRemote = plan.stagingRemote
	}

	if err := dev.Upload(pr, uploadRemote, 0o644); err != nil {
		return installResult{}, fmt.Errorf("upload: %w", err)
	}

	notify(installRunAppinst)

	if err := dev.Install(plan.appinstPath, plan.stagingRemote); err != nil {
		return installResult{}, fmt.Errorf("install: %w", err)
	}

	notify(installRescan)

	bundlePath, _, err := dev.FindInstalledByBundleID(plan.bundleID)
	if err != nil {
		return installResult{}, fmt.Errorf("post-install scan: %w", err)
	}

	if bundlePath == "" {
		return installResult{}, errors.New("install reported success but bundle not found")
	}

	return installResult{
		bundlePath:      bundlePath,
		installed:       true,
		reinstalled:     reinstalled,
		previousVersion: previousVersion,
	}, nil
}

func remoteOutputPath(bundleID, version, jailbreak string) string {
	return path.Join(device.RemoteRootForJailbreak(jailbreak), "decrypted", fmt.Sprintf("%s_%s.decrypted.ipa", bundleID, version))
}

func effectiveDecryptKeepPolicy(cfg *config.Config) (string, error) {
	if decryptKeep != "" {
		return config.NormalizeOutputKeep(decryptKeep)
	}

	return config.NormalizeOutputKeep(cfg.Output.Keep)
}

func decryptWorkingOutputPath(keepDesktop bool, bundleID, version string) (string, func(), error) {
	if keepDesktop {
		out, err := localOutputPath(decryptOutput, bundleID, version)
		return out, nil, err
	}

	tmp, err := os.CreateTemp("", fmt.Sprintf("ipadecrypt-%s-%s-*.ipa", safeFilename(bundleID), safeFilename(version)))
	if err != nil {
		return "", nil, err
	}

	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", nil, err
	}

	return name, func() { os.Remove(name) }, nil
}

func localOutputPath(override, bundleID, version string) (string, error) {
	defaultName := fmt.Sprintf("%s_%s.decrypted.ipa", bundleID, version)

	if override == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}

		return filepath.Join(home, "ipadecrypt", "decrypted", defaultName), nil
	}

	abs, err := filepath.Abs(override)
	if err != nil {
		return "", err
	}

	// if override is just a directory (not a full file path), place the default filename inside it
	info, err := os.Stat(abs)
	if err == nil && info.IsDir() {
		return filepath.Join(abs, defaultName), nil
	}

	return abs, nil
}

func safeFilename(s string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return replacer.Replace(s)
}

// runStoreKitInstall triggers an on-device StoreKit download (which surfaces
// as an "Install" prompt on the device that the user must confirm), then
// polls until the install changes. Detects re-installs (path/UUID changes)
// and version bumps. Returns true on success.
func runStoreKitInstall(dev *device.Client, bundleID, beforeVersion, jailbreak string) bool {
	live := tui.NewLive()
	live.Spin("uploading StoreKit helper")

	appdlPath, err := dev.EnsureAppdl(jailbreak)
	if err != nil {
		live.Fail("appdl upload: %v", err)
		return false
	}

	beforePath, _, _ := dev.FindInstalledByBundleID(bundleID)

	live.Spin("requesting App Store download for %s", bundleID)

	var lastErrorReason string
	code, err := dev.RunAppdl(appdlPath, bundleID, func(line string) {
		if strings.HasPrefix(line, "@evt ") {
			ev := strings.TrimPrefix(line, "@evt ")
			if strings.HasPrefix(ev, "event=error") {
				if r := extractAttr(ev, "reason"); r != "" {
					lastErrorReason = r
				}
			} else {
				tui.Info("  %s", ev)
			}
		}
	})
	if err != nil {
		live.Fail("appdl: %v", err)
		return false
	}
	if code != 0 {
		if lastErrorReason != "" {
			live.Fail("App Store rejected the download: %s", lastErrorReason)
			tui.Info("often this means the device's iOS is older than the app's MinimumOSVersion and no compatible build is available for this Apple ID")
		} else {
			live.Fail("appdl exit %d", code)
		}
		return false
	}

	live.OK("download requested - confirm the install prompt on your device")

	// Poll until install path changes (re-install gets a new UUID dir) or
	// version bumps. If the app was already at latest, the device may show
	// no prompt at all - so allow the user to skip the wait.
	live = tui.NewLive()
	live.Spin("waiting for install")

	disarm := func() {
		_, _, _, _ = dev.RunSudo("rm -f /var/mobile/.ipadecryptautoalert-arm")
	}

	deadline := time.Now().Add(3 * time.Minute)
	stableSince := time.Time{}
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		p, _, err := dev.FindInstalledByBundleID(bundleID)
		if err != nil || p == "" {
			stableSince = time.Time{}
			continue
		}
		v, _ := dev.InstalledVersion(p)

		// Re-install (new UUID) or version bump → success.
		if (beforePath != "" && p != beforePath) || (v != "" && v != beforeVersion) {
			live.OK("installed v%s", v)
			disarm()
			return true
		}

		// Same path & same version: either nothing happened or already latest.
		// Wait 15s of stable state before assuming "already latest" and proceeding.
		if stableSince.IsZero() {
			stableSince = time.Now()
		}
		if time.Since(stableSince) > 15*time.Second {
			live.OK("already at latest compatible v%s (no install needed)", beforeVersion)
			disarm()
			return true
		}
	}

	live.Fail("timed out waiting for install")
	disarm()
	return false
}

// extractAttr pulls a "key=\"value\"" or "key=value" out of a one-line @evt
// payload. Returns empty string if the key isn't found.
func extractAttr(line, key string) string {
	prefix := key + "="
	idx := strings.Index(line, prefix)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(prefix):]
	if strings.HasPrefix(rest, "\"") {
		rest = rest[1:]
		end := strings.Index(rest, "\"")
		if end < 0 {
			return rest
		}
		return rest[:end]
	}
	end := strings.Index(rest, " ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// resolveBundleByFuzzy first searches installed apps; if nothing matches,
// it falls back to the public iTunes search API.
func resolveBundleByFuzzy(dev *device.Client, as *appstore.Client, acc *appstore.Account, term string) (string, error) {
	live := tui.NewLive()
	live.Spin("searching installed apps for %q", term)

	apps, err := dev.SearchInstalledApps(term)
	if err != nil {
		live.Fail("search installed apps: %v", err)
		return "", err
	}

	needle := strings.ToLower(term)
	var matches []device.InstalledApp
	for _, a := range apps {
		if strings.Contains(strings.ToLower(a.BundleID), needle) ||
			strings.Contains(strings.ToLower(a.DisplayName), needle) {
			matches = append(matches, a)
		}
	}

	if len(matches) == 0 {
		live.OK("no installed match - searching App Store")
		return resolveBundleByAppStoreSearch(as, acc, term)
	}

	live.OK("%d installed match(es) for %q", len(matches), term)
	options := make([]string, len(matches)+1)
	for i, m := range matches {
		options[i] = fmt.Sprintf("%s — %s", m.BundleID, m.DisplayName)
	}
	options[len(matches)] = "(none of these — search App Store)"
	idx, err := tui.Select("pick the app to decrypt", options)
	if err != nil {
		return "", err
	}
	if idx == len(matches) {
		return resolveBundleByAppStoreSearch(as, acc, term)
	}
	return matches[idx].BundleID, nil
}

func resolveBundleByAppStoreSearch(as *appstore.Client, acc *appstore.Account, term string) (string, error) {
	live := tui.NewLive()
	live.Spin("App Store search for %q", term)

	results, err := as.Search(acc, term, 10)
	if err != nil {
		live.Fail("search: %v", err)
		return "", err
	}
	if len(results) == 0 {
		live.Fail("no App Store matches for %q", term)
		return "", fmt.Errorf("no match")
	}

	live.OK("%d App Store matches for %q", len(results), term)

	if len(results) == 1 {
		r := results[0]
		tui.Info("→ %s — %s by %s", r.BundleID, r.Name, r.ArtistName)
		return r.BundleID, nil
	}

	options := make([]string, len(results))
	for i, r := range results {
		options[i] = fmt.Sprintf("%s — %s by %s", r.BundleID, r.Name, r.ArtistName)
	}
	idx, err := tui.Select("pick the app to decrypt", options)
	if err != nil {
		return "", err
	}
	return results[idx].BundleID, nil
}

// cleanupStack is a LIFO of best-effort cleanup callbacks. Drained on normal
// return (via defer) and on SIGINT/SIGTERM. Idempotent: a second run() is a
// no-op so the deferred run after signal-driven run is harmless.
type cleanupStack struct {
	mu  sync.Mutex
	fns []func()
}

func (c *cleanupStack) push(fn func()) {
	c.mu.Lock()
	c.fns = append(c.fns, fn)
	c.mu.Unlock()
}

func (c *cleanupStack) run() {
	c.mu.Lock()
	fns := c.fns
	c.fns = nil
	c.mu.Unlock()

	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// HandleEvent renders one helper event into a TUI update. The helper
// embeds a human-readable `msg` attribute on every event, so this
// function mostly just relays msg as the note, with per-event spinner
// updates and counters layered on top.
func (p *helperProgress) HandleEvent(ev device.Event) helperUpdate {
	upd := helperUpdate{note: ev.Attr("msg")}

	switch ev.Name {
	// Spinner-only events: progress indicator, no note line.
	case "bundle.begin":
		upd.note = ""
		upd.spin = fmt.Sprintf("decrypting %s", path.Base(ev.Attr("src")))
	case "dyld.resuming":
		upd.note = ""
		upd.spin = "running target"
	case "image.begin":
		upd.note = ""
		upd.spin = fmt.Sprintf("decrypting %s", ev.Attr("name"))
	case "pack.begin":
		upd.note = ""

		ipa := ev.Attr("ipa")
		if ipa == "-" {
			upd.spin = "packaging IPA → stdout"
		} else {
			upd.spin = fmt.Sprintf("packaging IPA → %s", path.Base(ipa))
		}

	// Counters + spinner update on each successful dump.
	case "image.done":
		p.dumpedTotal.Add(1)

		switch ev.Attr("kind") {
		case "main":
			p.dumpedMain.Add(1)
		case "framework":
			p.dumpedFrameworks.Add(1)
		default:
			p.dumpedOther.Add(1)
		}

		upd.spin = fmt.Sprintf("decrypted %d image(s)", p.dumpedTotal.Load())

	// Suppress empty-result bundle.done so the TUI stays quiet on appex
	// passes that produce no extras.
	case "bundle.done":
		if ev.Attr("extras") == "0" {
			upd.note = ""
		}

	// Trap on the benign dyld halt brk is silent  the helper catches it
	// on purpose so the address space stays readable; nothing to surface.
	case "dyld.trapped":
		if ev.Attr("exception") == "EXC_BREAKPOINT" {
			upd.note = ""
		}

	// Pure diagnostics that show up only in --verbose runs.
	case "target.csflags",
		"patch.scan_skipped", "patch.skipped", "patch.dyld_base_diff",
		"dyld.settled", "dyld.fault_skip", "dyld.pac_stripped",
		"staging.begin", "done":
		upd.note = ""
	}

	// Catch-all for unknown event names: fall back to msg attr (already
	// set above). If a future event lacks msg, render attrs verbatim so
	// nothing is silently lost.
	if upd.note == "" && upd.spin == "" && ev.Attr("msg") == "" {
		parts := []string{"event=" + ev.Name}
		for k, v := range ev.Attrs {
			if k == "event" || k == "level" {
				continue
			}

			parts = append(parts, fmt.Sprintf("%s=%q", k, v))
		}

		upd.note = strings.Join(parts, " ")
	}

	return upd
}

func (p *helperProgress) Summary() string {
	total := p.dumpedTotal.Load()
	main := p.dumpedMain.Load()
	frameworks := p.dumpedFrameworks.Load()
	other := p.dumpedOther.Load()

	summary := fmt.Sprintf("decrypted %d image(s): %d main, %d framework", total, main, frameworks)
	if other > 0 {
		summary += fmt.Sprintf(", %d other", other)
	}

	return summary
}
