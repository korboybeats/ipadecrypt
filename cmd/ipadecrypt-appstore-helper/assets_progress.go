package main

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/londek/ipadecrypt/internal/sap/assets"
)

func ensureAppleAssets(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	var mu sync.Mutex
	var lastEmit time.Time
	var lastStage string

	ctx = assets.WithProgress(ctx, func(p assets.Progress) {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		stageChanged := p.Stage != lastStage
		if !stageChanged && !lastEmit.IsZero() && now.Sub(lastEmit) < 250*time.Millisecond {
			return
		}
		lastEmit = now
		lastStage = p.Stage

		kv := []string{
			"phase", "asset-progress",
			"stage", p.Stage,
			"message", p.Message,
			"current", strconv.FormatInt(p.Current, 10),
			"total", strconv.FormatInt(p.Total, 10),
			"needed", strconv.FormatInt(p.Needed, 10),
		}
		emit(kv...)
	})

	emit("phase", "step", "name", "preparing-machine-identity")
	if _, err := assets.Load(ctx); err != nil {
		return fmt.Errorf("prepare Apple machine-identity support files: %w", err)
	}
	stats := assets.LastLoadStats()
	emit("phase", "asset-ready",
		"cache", mustCacheDir(),
		"fetched", strconv.FormatInt(stats.Fetched, 10),
		"needed", strconv.FormatInt(stats.Needed, 10),
	)
	return nil
}

func mustCacheDir() string {
	dir, err := assets.CacheDirectory()
	if err != nil {
		return ""
	}
	return dir
}

func runPrefetchAssets() error {
	emit("phase", "step", "name", "prefetch-assets")
	if assets.CacheValid() {
		emit("phase", "asset-ready", "cache", mustCacheDir(), "status", "already-cached", "fetched", "0", "needed", "0")
		return nil
	}
	if err := ensureAppleAssets(context.Background()); err != nil {
		return err
	}
	emit("phase", "done", "name", "prefetch-assets")
	return nil
}
