package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/londek/ipadecrypt/internal/sap/assets"
	"github.com/londek/ipadecrypt/internal/sap/machine"
)

// Synthetic 16-byte host UUID used only by --probe-modern-adi. It is not an
// Apple ID and is never sent to Apple.
var probeModernADIIdentifier = []byte("ipadecrypt-probe")

// Matches storeauth.machineDSID; used only for a local OTP/header computation.
const probeMachineDSID = ^uint64(1)

const probeModernADICycles = 3

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

	for cycle := 1; cycle <= probeModernADICycles; cycle++ {
		emit("phase", "step", "name", "open-modern-adi", "cycle", strconv.Itoa(cycle))
		adi, err := machine.OpenModernADI(ctx, bundle, probeModernADIIdentifier, nil)
		if err != nil {
			return fmt.Errorf("open modern ADI (cycle %d): %w", cycle, err)
		}

		// Local FairPlay header/OTP computation only — no Apple network call.
		if _, _, otpErr := adi.RequestOTP(probeMachineDSID); otpErr != nil {
			emit("phase", "step", "name", "otp-unprovisioned", "cycle", strconv.Itoa(cycle))
		} else {
			emit("phase", "step", "name", "otp-ok", "cycle", strconv.Itoa(cycle))
		}

		if err := adi.Close(); err != nil {
			return fmt.Errorf("close modern ADI (cycle %d): %w", cycle, err)
		}
		emit("phase", "step", "name", "closed-modern-adi", "cycle", strconv.Itoa(cycle))
	}

	emit("phase", "done", "name", "probe-modern-adi", "cycles", strconv.Itoa(probeModernADICycles))
	return nil
}
