package appstore

import (
	"fmt"
	"net/http"
	"net/url"
)

func isNumericID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

type lookupResult struct {
	Results []App `json:"results,omitempty"`
}

// Lookup resolves a bundle ID or App Store track ID to an App via the public
// iTunes search API. If id is all digits it is treated as a numeric trackId,
// otherwise as a bundle identifier. The account is used only to pick the
// storefront (country code).
func (c *Client) Lookup(acc Account, id string) (App, error) {
	cc, err := countryCodeFromStoreFront(acc.StoreFront)
	if err != nil {
		return App{}, err
	}

	q := url.Values{}
	q.Add("entity", "software,iPadSoftware")
	q.Add("limit", "1")
	q.Add("media", "software")
	if isNumericID(id) {
		q.Add("id", id)
	} else {
		q.Add("bundleId", id)
	}
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
		return App{}, fmt.Errorf("lookup: %s not found", id)
	}

	return out.Results[0], nil
}
