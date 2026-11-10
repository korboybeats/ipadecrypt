package storeauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/londek/ipadecrypt/internal/sap/assets"
	"github.com/londek/ipadecrypt/internal/sap/machine"
	"github.com/londek/ipadecrypt/internal/store/emulator"
	"github.com/londek/ipadecrypt/internal/store/profile"
)

// StoreAMDHeaders executes AppleMediaServices' type-2 anisette flow and its
// com.apple.adid dependency entirely inside the pinned Unicorn runtimes.
func StoreAMDHeaders(ctx context.Context, encoded []byte, endpoint string, hardwareID []byte) (map[string]string, []byte, error) {
	if ctx == nil {
		return nil, nil, errors.New("store AMD context is nil")
	}

	state, err := decodeAnisetteState(encoded)
	if err != nil {
		return nil, nil, err
	}

	if len(state.Provisioning) == 0 {
		return nil, nil, ErrNotProvisioned
	}

	return runStoreAMDHeaders(ctx, state, endpoint, hardwareID)
}

func runStoreAMDHeaders(ctx context.Context, state *anisetteState, endpoint string, hardwareID []byte) (map[string]string, []byte, error) {
	bundle, err := assets.Load(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("load Store AMD assets: %w", err)
	}

	adi, err := machine.OpenModernADI(ctx, bundle, state.Identifier, state.Provisioning)
	if err != nil {
		return nil, nil, fmt.Errorf("open Store AMD service: %w", err)
	}

	defer func() { _ = adi.Close() }()

	prepared, err := profile.Load(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("load Store AMD framework profile: %w", err)
	}

	framework, err := emulator.Open(ctx, bundle.AppleMediaServices, prepared)
	if err != nil {
		return nil, nil, fmt.Errorf("open Store AMD framework: %w", err)
	}

	defer func() { _ = framework.Close() }()

	headers, err := framework.StoreAMDHeaders(endpoint, hardwareID, func(service string, request []byte) ([]byte, error) {
		if service != "com.apple.adid" {
			return nil, fmt.Errorf("unsupported Store AMD XPC service %s", service)
		}

		return adi.ExchangeXPC(request)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("run Store AMD framework: %w", err)
	}

	state.Provisioning, err = adi.State()
	if err != nil {
		return nil, nil, fmt.Errorf("persist Store AMD state: %w", err)
	}

	updated, err := json.Marshal(state)
	if err != nil {
		return nil, nil, errors.New("encode Store AMD state")
	}

	return headers, updated, nil
}

// probeStoreAMDIdentifier is a synthetic 16-byte host UUID used only by
// ProbeStoreAMDHeaders. It is not an Apple ID and is never sent to Apple.
var probeStoreAMDIdentifier = []byte("ipadecrypt-probe")

const probeStoreAMDEndpoint = "https://buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/authenticate"

// ProbeStoreAMDHeaders runs the Store AMD / storeAMDLoginProof header path
// end to end with a dummy identity: OpenModernADI, the AMS emulator,
// XPC ExchangeXPC, and header assembly. It does not provision, persist
// state, or perform HTTP. A guest/XPC error still proves the nested
// start path ran if the process survives.
func ProbeStoreAMDHeaders(ctx context.Context) error {
	if ctx == nil {
		return errors.New("store AMD probe context is nil")
	}
	if len(probeStoreAMDIdentifier) != 16 {
		return errors.New("store AMD probe identifier must be 16 bytes")
	}

	_, _, err := runStoreAMDHeaders(ctx, &anisetteState{Identifier: probeStoreAMDIdentifier}, probeStoreAMDEndpoint, []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06})
	return err
}
