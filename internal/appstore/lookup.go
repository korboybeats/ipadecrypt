package appstore

import (
	"fmt"
	"net/http"
	"net/url"
)

type lookupResult struct {
	Results []App `json:"results,omitempty"`
}

// Lookup resolves a bundle ID to an App via the public iTunes search API.
// The account is used only to pick the storefront (country code).
func (c *Client) Lookup(acc Account, bundleID string) (App, error) {
	cc, err := countryCodeFromStoreFront(acc.StoreFront)
	if err != nil {
		return App{}, err
	}

	q := url.Values{}
	q.Add("entity", "software,iPadSoftware")
	q.Add("limit", "1")
	q.Add("media", "software")
	q.Add("bundleId", bundleID)
	q.Add("country", cc)

	u := fmt.Sprintf("https://%s%s?%s", iTunesDomain, lookupPath, q.Encode())

	var out lookupResult
	res, err := c.send(http.MethodGet, u, nil, nil, formatJSON, &out)
	if err != nil {
		return App{}, fmt.Errorf("lookup: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return App{}, fmt.Errorf("lookup: status %d", res.StatusCode)
	}

	if len(out.Results) == 0 {
		return App{}, fmt.Errorf("lookup: %s not found", bundleID)
	}

	return out.Results[0], nil
}
