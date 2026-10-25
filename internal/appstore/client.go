package appstore

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	cookiejar "github.com/juju/persistent-cookiejar"
	apphttp "github.com/londek/ipadecrypt/internal/apphttp"
	"github.com/londek/ipadecrypt/internal/store"
)

type Client struct {
	jar                 *cookiejar.Jar
	http                *http.Client
	backend             *store.Backend
	actionSignerFactory ActionSignerFactory
	kbsync              kbsyncStore

	// executorFor overrides the backend executor per operation (tests only).
	executorFor func(op string) apphttp.Executor
}

// executor returns the request executor for the given operation label.
func (c *Client) executor(op string) apphttp.Executor {
	if c.executorFor != nil {
		return c.executorFor(op)
	}

	return c.backend.Client(op)
}

func New(cookiesFile string) (*Client, error) {
	if cookiesFile != "" {
		if err := os.MkdirAll(filepath.Dir(cookiesFile), 0o755); err != nil {
			return nil, err
		}
	}

	jar, err := cookiejar.New(&cookiejar.Options{Filename: cookiesFile})
	if err != nil {
		return nil, err
	}

	// Plain client for CDN byte transfers (signed URLs; no attestation needed).
	hc := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if strings.TrimSuffix(req.Referer(), "/") == authURL {
				return http.ErrUseLastResponse
			}

			return nil
		},
	}

	// Store API requests are built and attested by the emulated AppleMediaServices
	// framework in the backend; the SAP signer (used for authenticate) runs its
	// own setup/cert exchange through the backend's "api" executor.
	backend := store.New(jar, macAddress)

	return &Client{
		jar:                 jar,
		http:                hc,
		backend:             backend,
		actionSignerFactory: frameworkActionSignerFactory(backend.Client("api")),
	}, nil
}

// guid returns the Configurator-shaped GUID: uppercase MAC address, no colons.
func guid() (string, error) {
	mac, err := macAddress()
	if err != nil {
		return "", err
	}

	return strings.ReplaceAll(strings.ToUpper(mac), ":", ""), nil
}

func macAddress() (string, error) {
	if iface, err := net.InterfaceByName("en0"); err == nil && iface.HardwareAddr != nil {
		if s := iface.HardwareAddr.String(); s != "" {
			return s, nil
		}
	}

	ifs, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, ni := range ifs {
		if s := ni.HardwareAddr.String(); s != "" {
			return s, nil
		}
	}

	return "", errors.New("no network interface with a MAC address")
}
