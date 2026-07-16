package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/londek/ipadecrypt/internal/appstore"
	"github.com/londek/ipadecrypt/internal/config"
	"github.com/londek/ipadecrypt/internal/tui"
	"github.com/londek/ipadecrypt/internal/updater"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func downloadHandler(cmd *cobra.Command, args []string) {
	if downloadSelectVersion && downloadExtVerID != "" {
		tui.Err("--select-version and --external-version-id are mutually exclusive; pass at most one.")
		return
	}

	if downloadSelectVersion && !term.IsTerminal(int(os.Stdin.Fd())) {
		tui.Err("download --select-version requires a terminal")
		return
	}

	cfg, paths, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	upd := updater.Start(context.Background(), Version, cfg)
	defer upd.Wait()

	target, err := parseStoreTargetArg(args[0], "download")
	if err != nil {
		tui.Err("%v", err)
		return
	}

	if cfg.Apple.PasswordToken == "" || cfg.Apple.DirectoryServicesIdentifier == "" {
		tui.Err("environment not configured")
		tui.Info("run `ipadecrypt bootstrap` first to sign in")
		return
	}

	as, err := appstore.New(filepath.Join(paths.Root, "cookies"))
	if err != nil {
		tui.Err("appstore client: %v", err)
		return
	}

	live := tui.NewLive()
	if target.appId != "" {
		live.Spin("resolving appId %s", target.appId)
	} else {
		live.Spin("resolving bundleId %s", target.bundleId)
	}

	app, err := lookupStoreTargetApp(as, cfg.Apple.Account(), target)
	if err != nil {
		live.Fail("lookup failed: %v", err)
		return
	}

	if app.Price > 0 {
		live.Fail("paid app (price=%v) - unsupported", app.Price)
		return
	}

	live.OK("found %s on App Store", app.BundleID)

	if downloadSelectVersion {
		downloadSelectedVersions(cfg, paths, as, app)
		return
	}

	disposition, err := fetchDownloadSource(cfg, paths, as, app, downloadExtVerID, "")
	if err != nil {
		return
	}

	outLocal, err := downloadOutputPath(paths, downloadOutput, app.BundleID, disposition.version)
	if err != nil {
		tui.Err("output path: %v", err)
		return
	}

	if err := writeDownloadedIPA(disposition.path, outLocal, disposition.kind, ""); err != nil {
		tui.Err("%v", err)
	}
}

func downloadSelectedVersions(cfg *config.Config, paths *config.Paths, as *appstore.Client, app appstore.App) {
	if err := ensureVersionsWarningAccepted(cfg); err != nil {
		if err.Error() != "aborted" {
			tui.Err("%v", err)
		}
		return
	}

	live := tui.NewLive()
	live.Spin("listing versions for %s", app.BundleID)

	list, err := listVersionsWithAuth(cfg, as, app, "")
	if err != nil {
		live.Fail("list versions failed: %v", err)
		return
	}

	live.OK("%d version(s), latest %s", len(list.ExternalVersionIDs), list.LatestExternalVersionID)

	cache, cachePath, err := loadOrInitVersionsCache(paths, app.BundleID)
	if err != nil {
		tui.Err("cache path: %v", err)
		return
	}

	selectedExtVerIDs, err := runVersionsPicker(cfg, as, app, list, cache, cachePath, "", "")
	if err != nil {
		if errors.Is(err, errVersionSelectionAborted) {
			return
		}

		tui.Err("%v", err)
		return
	}

	outDir, err := multiDownloadOutputDir(paths, downloadOutput)
	if err != nil {
		tui.Err("output path: %v", err)
		return
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		tui.Err("mkdir %s: %v", outDir, err)
		return
	}

	for i, extVerID := range selectedExtVerIDs {
		phase := fmt.Sprintf("%d/%d", i+1, len(selectedExtVerIDs))

		disposition, err := fetchDownloadSource(cfg, paths, as, app, extVerID, phase)
		if err != nil {
			return
		}

		outLocal := filepath.Join(outDir, fmt.Sprintf("%s_%s.ipa", app.BundleID, disposition.version))
		if err := writeDownloadedIPA(disposition.path, outLocal, disposition.kind, phase); err != nil {
			tui.Err("%v", err)
			return
		}
	}
}

func fetchDownloadSource(cfg *config.Config, paths *config.Paths, as *appstore.Client, app appstore.App, extVerID, phase string) (remoteSourceDisposition, error) {
	live := tui.NewLive()
	live.Spin("%s", phaseLabel("fetching download metadata", phase))

	disposition, err := fetchRemoteEncryptedSource(cfg, paths, as, app, extVerID, "", func(e authEvent) {
		switch e {
		case authReauth:
			live.Spin("%s", phaseLabel("re-authenticating", phase))
		case authLicense:
			live.Spin("%s", phaseLabel("acquiring license", phase))
		case authRetryingDownload:
			live.Spin("%s", phaseLabel("retrying download", phase))
		}
	}, func(cur, total int64) {
		live.Message("%s", transferProgressText(phaseLabel("downloading IPA from App Store", phase), cur, total))
		live.Progress(cur, total)
	})
	if err != nil {
		if errors.Is(err, errRemoteDownloadFailed) {
			live.Fail("download failed: %v", errors.Unwrap(err))
			return remoteSourceDisposition{}, err
		}

		live.Fail("prepare failed: %v", err)
		return remoteSourceDisposition{}, err
	}

	if disposition.kind == sourceDispositionCached {
		live.OK("%s", phaseLabel(fmt.Sprintf("cached %s", filepath.Base(disposition.path)), phase))
	} else {
		live.OK("%s", phaseLabel(fmt.Sprintf("downloaded %s", filepath.Base(disposition.path)), phase))
	}

	return disposition, nil
}

func writeDownloadedIPA(srcPath, outLocal string, kind sourceDisposition, phase string) error {
	if sameFilePath(srcPath, outLocal) {
		tui.OK("-> %s (%s)", outLocal, sourceDispositionLabel(kind))
		return nil
	}

	live := tui.NewLive()
	live.Spin("%s", phaseLabel(fmt.Sprintf("writing -> %s", filepath.Base(outLocal)), phase))

	if err := copyFileWithProgress(srcPath, outLocal, func(cur, total int64) {
		live.Message("%s", transferProgressText(phaseLabel(fmt.Sprintf("writing -> %s", filepath.Base(outLocal)), phase), cur, total))
		live.Progress(cur, total)
	}); err != nil {
		live.Fail("write failed: %v", err)
		return fmt.Errorf("write %s: %w", outLocal, err)
	}

	live.OK("-> %s (%s)", outLocal, sourceDispositionLabel(kind))
	return nil
}

func downloadOutputPath(paths *config.Paths, override, bundleID, version string) (string, error) {
	defaultName := fmt.Sprintf("%s_%s.ipa", bundleID, version)

	if override == "" {
		return filepath.Join(paths.Root, defaultName), nil
	}

	abs, err := filepath.Abs(override)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(abs)
	if err == nil && info.IsDir() {
		return filepath.Join(abs, defaultName), nil
	}

	return abs, nil
}

func multiDownloadOutputDir(paths *config.Paths, override string) (string, error) {
	if override == "" {
		return paths.Root, nil
	}

	abs, err := filepath.Abs(override)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(abs)
	if err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("%s is a file; multi-select download expects a directory", abs)
		}

		return abs, nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	return abs, nil
}

func copyFileWithProgress(srcPath, dstPath string, onProgress func(cur, total int64)) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcPath, err)
	}
	defer src.Close()

	st, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", srcPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dstPath), err)
	}

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", dstPath, err)
	}

	pw := newProgressWriter(dst, st.Size(), onProgress)
	if _, err := io.Copy(pw, src); err != nil {
		dst.Close()
		return fmt.Errorf("copy %s -> %s: %w", srcPath, dstPath, err)
	}

	if err := dst.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dstPath, err)
	}

	pw.Flush()
	return nil
}

func phaseLabel(base, phase string) string {
	if phase == "" {
		return base
	}

	return fmt.Sprintf("%s [%s]", base, phase)
}

func sourceDispositionLabel(kind sourceDisposition) string {
	switch kind {
	case sourceDispositionCached:
		return "cached"
	case sourceDispositionDownloaded:
		return "downloaded"
	default:
		return "unknown"
	}
}

func sameFilePath(a, b string) bool {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false
	}

	absB, err := filepath.Abs(b)
	if err != nil {
		return false
	}

	return filepath.Clean(absA) == filepath.Clean(absB)
}
