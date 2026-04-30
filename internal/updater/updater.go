// Package updater implements a small, opt-out GitHub Releases update checker.
// State (last-checked timestamp, last-seen tag, last-notified timestamp) is
// persisted in the main config.json under the "updateCheck" key.
package updater

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/londek/ipadecrypt/internal/config"
	"github.com/londek/ipadecrypt/internal/tui"
	"golang.org/x/mod/semver"
)

const (
	envDisable    = "IPADECRYPT_NO_UPDATE_CHECK"
	checkInterval = 24 * time.Hour
	notifyEvery   = 24 * time.Hour
)

// IsDev reports whether the running binary was built without an injected
// version (i.e. main.Version == "dev" or empty).
func IsDev(current string) bool {
	return current == "" || current == "dev"
}

// Disabled reports whether the user has opted out via env var.
func Disabled() bool {
	v := os.Getenv(envDisable)
	return v != "" && v != "0" && v != "false"
}

// Check performs a network query (or returns the fresh cached result from
// cfg.UpdateCheck) and returns the latest release plus whether it is strictly
// newer than current. When force is true, the freshness window is bypassed.
// On a successful network call the cfg is updated in place and saved.
func Check(ctx context.Context, current string, cfg *config.Config, force bool) (rel *Release, newer bool, err error) {
	if !force {
		uc := cfg.UpdateCheck
		if uc.LatestTag != "" && time.Since(uc.CheckedAt) < checkInterval {
			rel = &Release{Tag: uc.LatestTag, HTMLURL: uc.HTMLURL}
			return rel, isNewer(current, uc.LatestTag), nil
		}
	}

	rel, err = fetchLatest(ctx, current)
	if err != nil {
		return nil, false, err
	}

	cfg.UpdateCheck.CheckedAt = time.Now().UTC()
	cfg.UpdateCheck.LatestTag = rel.Tag
	cfg.UpdateCheck.HTMLURL = rel.HTMLURL
	cfg.Save()

	return rel, isNewer(current, rel.Tag), nil
}

func isNewer(current, latest string) bool {
	if IsDev(current) {
		return false
	}

	// semver.Compare returns 0 on invalid input, which collapses to "not newer".
	return semver.Compare(current, latest) < 0
}

// Async runs a background update check tied to a single command invocation.
// Call Wait at the end of the command to print the trailing notice (if any).
type Async struct {
	wg      sync.WaitGroup
	mu      sync.Mutex
	rel     *Release
	current string
	cfg     *config.Config
	skipped bool
}

// Start spawns a background check. Safe to call even when disabled — it
// short-circuits and Wait becomes a no-op.
func Start(ctx context.Context, current string, cfg *config.Config) *Async {
	a := &Async{current: current, cfg: cfg}
	if IsDev(current) || Disabled() || cfg == nil || !tui.IsTTY() {
		a.skipped = true
		return a
	}

	a.wg.Go(func() {
		ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()

		rel, newer, err := Check(ctx, current, cfg, false)
		if err != nil || !newer || rel == nil {
			return
		}

		a.mu.Lock()
		a.rel = rel
		a.mu.Unlock()
	})

	return a
}

// Wait blocks for the background check, then prints a one-line notice when a
// newer release exists and the user has not been notified within notifyEvery.
func (a *Async) Wait() {
	if a == nil || a.skipped {
		return
	}

	a.wg.Wait()

	a.mu.Lock()
	rel := a.rel
	a.mu.Unlock()

	if rel == nil {
		return
	}

	uc := a.cfg.UpdateCheck

	now := time.Now().UTC()
	if uc.LatestTag == rel.Tag && !uc.NotifiedAt.IsZero() && now.Sub(uc.NotifiedAt) < notifyEvery {
		return
	}

	tui.Warn("ipadecrypt %s available (you have %s) - %s", rel.Tag, a.current, rel.HTMLURL)

	a.cfg.UpdateCheck.NotifiedAt = now
	a.cfg.Save()
}
