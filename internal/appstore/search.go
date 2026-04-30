package appstore

import (
	"fmt"
	"net/http"
	"net/url"
)

// SearchResult is one hit from the iTunes search endpoint. Carries enough
// info to disambiguate app-name collisions (e.g. several apps called
// "Messenger" by different developers).
type SearchResult struct {
	ID                 int64   `json:"trackId,omitempty"`
	BundleID           string  `json:"bundleId,omitempty"`
	Name               string  `json:"trackName,omitempty"`
	ArtistName         string  `json:"artistName,omitempty"`
	Version            string  `json:"version,omitempty"`
	Price              float64 `json:"price,omitempty"`
	MinimumOSVersion   string  `json:"minimumOsVersion,omitempty"`
	GenreName          string  `json:"primaryGenreName,omitempty"`
	UserRatingCount    int     `json:"userRatingCount,omitempty"`
	AverageUserRating  float64 `json:"averageUserRating,omitempty"`
}

type searchResponse struct {
	ResultCount int            `json:"resultCount,omitempty"`
	Results     []SearchResult `json:"results,omitempty"`
}

// Search hits the public iTunes search API for software matching `term`.
// Caps results at `limit` (max 200 per Apple). No auth required.
func (c *Client) Search(acc *Account, term string, limit int) ([]SearchResult, error) {
	if limit <= 0 || limit > 200 {
		limit = 10
	}
	cc, err := CountryCodeFromStoreFront(acc.StoreFront)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Add("term", term)
	q.Add("entity", "software,iPadSoftware")
	q.Add("media", "software")
	q.Add("country", cc)
	q.Add("limit", fmt.Sprintf("%d", limit))

	u := fmt.Sprintf("https://%s/search?%s", iTunesDomain, q.Encode())

	var out searchResponse
	res, err := c.send(http.MethodGet, u, nil, nil, formatJSON, &out)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search: status %d", res.StatusCode)
	}
	return out.Results, nil
}
