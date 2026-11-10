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

	ctx = assets.WithProgress(ctx, func(p assets.Progress) {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		if !lastEmit.IsZero() && now.Sub(lastEmit) < 400*time.Millisecond && p.Stage == "downloading-support-files" {
			return
		}
		lastEmit = now

		kv := []string{
			"phase", "asset-progress",
			"stage", p.Stage,
			"message", p.Message,
			"current", strconv.FormatInt(p.Current, 10),
			"total", strconv.FormatInt(p.Total, 10),
		}
		emit(kv...)
	})

	emit("phase", "step", "name", "preparing-machine-identity")
	if _, err := assets.Load(ctx); err != nil {
		return fmt.Errorf("prepare Apple machine-identity support files: %w", err)
	}
	emit("phase", "asset-ready", "cache", mustCacheDir())
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
		emit("phase", "asset-ready", "cache", mustCacheDir(), "status", "already-cached")
		return nil
	}
	if err := ensureAppleAssets(context.Background()); err != nil {
		return err
	}
	emit("phase", "done", "name", "prefetch-assets")
	return nil
}
