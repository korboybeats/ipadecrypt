package main

import (
	"context"
	"fmt"

	"github.com/londek/ipadecrypt/internal/sap/assets"
	"github.com/londek/ipadecrypt/internal/sap/machine"
)

// Synthetic 16-byte host UUID used only by --probe-modern-adi. It is not an
// Apple ID and is never sent to Apple.
var probeModernADIIdentifier = []byte("ipadecrypt-probe")

func runProbeModernADI() error {
	emit("phase", "step", "name", "probe-modern-adi")
	if !assets.CacheValid() {
		return fmt.Errorf("cached Apple support files are not present; run --prefetch-assets first")
	}

	ctx := context.Background()
	bundle, err := assets.Load(ctx)
	if err != nil {
		return fmt.Errorf("load cached Apple support files: %w", err)
	}

	emit("phase", "step", "name", "open-modern-adi")
	adi, err := machine.OpenModernADI(ctx, bundle, probeModernADIIdentifier, nil)
	if err != nil {
		return fmt.Errorf("open modern ADI: %w", err)
	}
	_ = adi.Close()

	emit("phase", "done", "name", "probe-modern-adi")
	return nil
}
