package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/londek/ipadecrypt/internal/sap/assets"
	"github.com/londek/ipadecrypt/internal/storeauth"
)

const probeStoreAMDCycles = 2

// refuseAppleTransport fails any HTTP so this probe cannot contact Apple.
type refuseAppleTransport struct{}

func (refuseAppleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := ""
	if req != nil && req.URL != nil {
		host = req.URL.Scheme + "://" + req.URL.Host + req.URL.Path
	}
	return nil, fmt.Errorf("probe-store-amd: blocked HTTP to %s", host)
}

func installProbeHTTPStub() {
	stub := refuseAppleTransport{}
	http.DefaultTransport = stub
	if http.DefaultClient == nil {
		http.DefaultClient = &http.Client{}
	}
	http.DefaultClient.Transport = stub
	http.DefaultClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("probe-store-amd: blocked HTTP redirect")
	}
}

func runProbeStoreAMD() error {
	installProbeHTTPStub()
	emit("phase", "step", "name", "probe-store-amd")

	if !assets.CacheValid() {
		return fmt.Errorf("cached Apple support files are not present; run --prefetch-assets first")
	}

	ctx := context.Background()
	for cycle := 1; cycle <= probeStoreAMDCycles; cycle++ {
		emit("phase", "step", "name", "store-amd-headers", "cycle", strconv.Itoa(cycle))
		err := storeauth.ProbeStoreAMDHeaders(ctx)
		if err == nil {
			emit("phase", "step", "name", "store-amd-headers-ok", "cycle", strconv.Itoa(cycle))
			continue
		}
		// A guest/XPC error means the legacyLogin header path (emulator hook
		// -> ExchangeXPC -> nested ADI start) ran and the process survived.
		// Missing assets or a failed runtime open is a real probe failure.
		if isStoreAMDPathError(err) {
			emit("phase", "step", "name", "store-amd-headers-guest", "cycle", strconv.Itoa(cycle))
			continue
		}
		return fmt.Errorf("store AMD headers (cycle %d): %w", cycle, err)
	}

	emit("phase", "done", "name", "probe-store-amd", "cycles", strconv.Itoa(probeStoreAMDCycles))
	return nil
}

func isStoreAMDPathError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, p := range []string{
		"run Store AMD framework",
		"generate Store AMD proof",
		"create Store AMD request",
		"export Store AMD proof",
		"guest bridge",
		"ExchangeXPC",
		"XPC",
	} {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}
