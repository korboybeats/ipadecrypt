package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/londek/ipadecrypt/internal/config"
)

type SelfUpdateResult struct {
	CurrentVersion string
	LatestVersion  string
	AssetName      string
	ExecutablePath string
	BackupPath     string
	ReleaseURL     string
	Updated        bool
}

func CheckLatest(ctx context.Context, current string, cfg *config.Config) (*SelfUpdateResult, error) {
	rel, newer, err := Check(ctx, current, cfg, true)
	if err != nil {
		return nil, err
	}

	return &SelfUpdateResult{
		CurrentVersion: current,
		LatestVersion:  rel.Tag,
		AssetName:      currentAssetName(rel.Tag),
		ReleaseURL:     rel.HTMLURL,
		Updated:        newer,
	}, nil
}

func SelfUpdate(ctx context.Context, current string, cfg *config.Config) (*SelfUpdateResult, error) {
	rel, newer, err := Check(ctx, current, cfg, true)
	if err != nil {
		return nil, err
	}
	if !shouldInstallUpdate(current, newer) {
		return &SelfUpdateResult{
			CurrentVersion: current,
			LatestVersion:  rel.Tag,
			ReleaseURL:     rel.HTMLURL,
			Updated:        false,
		}, nil
	}

	assetName := currentAssetName(rel.Tag)
	asset, ok := rel.FindAsset(assetName)
	if !ok {
		return nil, fmt.Errorf("release %s has no asset %s", rel.Tag, assetName)
	}

	checksumsAsset, ok := rel.FindAsset("checksums.txt")
	if !ok {
		return nil, fmt.Errorf("release %s has no checksums.txt", rel.Tag)
	}

	checksumData, err := downloadAsset(ctx, checksumsAsset.BrowserDownloadURL)
	if err != nil {
		return nil, fmt.Errorf("download checksums.txt: %w", err)
	}

	checksums := parseChecksums(checksumData)
	want := checksums[assetName]
	if want == "" {
		return nil, fmt.Errorf("checksums.txt has no entry for %s", assetName)
	}

	binaryData, err := downloadAsset(ctx, asset.BrowserDownloadURL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", assetName, err)
	}
	if err := verifySHA256(binaryData, want); err != nil {
		return nil, err
	}

	exe, err := currentExecutablePath()
	if err != nil {
		return nil, err
	}

	backup := backupPathFor(exe)
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("windows self-update cannot replace the running executable; downloaded asset verified as %s", assetName)
	}

	if err := replaceExecutable(exe, backup, binaryData); err != nil {
		return nil, err
	}

	return &SelfUpdateResult{
		CurrentVersion: current,
		LatestVersion:  rel.Tag,
		AssetName:      assetName,
		ExecutablePath: exe,
		BackupPath:     backup,
		ReleaseURL:     rel.HTMLURL,
		Updated:        true,
	}, nil
}

func shouldInstallUpdate(current string, newer bool) bool {
	return IsDev(current) || newer
}

func RollbackCurrentExecutable() (*SelfUpdateResult, error) {
	exe, err := currentExecutablePath()
	if err != nil {
		return nil, err
	}

	backup := backupPathFor(exe)
	if err := rollbackExecutable(exe, backup); err != nil {
		return nil, err
	}

	return &SelfUpdateResult{
		ExecutablePath: exe,
		BackupPath:     backup,
		Updated:        true,
	}, nil
}

func currentExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	return exe, nil
}

func assetNameForPlatform(tag, goos, goarch string) string {
	version := strings.TrimPrefix(tag, "v")
	name := fmt.Sprintf("ipadecrypt_%s_%s_%s", version, goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func currentAssetName(tag string) string {
	return assetNameForPlatform(tag, runtime.GOOS, runtime.GOARCH)
}

func downloadAsset(ctx context.Context, url string) ([]byte, error) {
	if url == "" {
		return nil, errors.New("asset download URL is empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ipadecrypt-updater")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download asset: status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func parseChecksums(data []byte) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			out[fields[1]] = fields[0]
		}
	}
	return out
}

func verifySHA256(data []byte, want string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

func backupPathFor(exe string) string {
	return exe + ".bak"
}

func replaceExecutable(exe, backup string, data []byte) error {
	info, err := os.Stat(exe)
	if err != nil {
		return fmt.Errorf("stat current executable: %w", err)
	}

	tmp := exe + ".new"
	if err := os.WriteFile(tmp, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write new executable: %w", err)
	}

	if err := os.Chmod(tmp, info.Mode().Perm()|0o111); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod new executable: %w", err)
	}

	_ = os.Remove(backup)
	if err := os.Rename(exe, backup); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup current executable: %w", err)
	}

	if err := os.Rename(tmp, exe); err != nil {
		restoreErr := os.Rename(backup, exe)
		if restoreErr != nil {
			return fmt.Errorf("replace executable: %w; rollback failed: %v", err, restoreErr)
		}
		return fmt.Errorf("replace executable: %w", err)
	}

	return nil
}

func rollbackExecutable(exe, backup string) error {
	if _, err := os.Stat(backup); err != nil {
		return fmt.Errorf("backup not available: %w", err)
	}

	tmp := exe + ".rollback"
	_ = os.Remove(tmp)
	if err := os.Rename(exe, tmp); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("move current executable aside: %w", err)
	}

	if err := os.Rename(backup, exe); err != nil {
		if _, statErr := os.Stat(tmp); statErr == nil {
			_ = os.Rename(tmp, exe)
		}
		return fmt.Errorf("restore backup: %w", err)
	}

	_ = os.Remove(tmp)
	return nil
}
