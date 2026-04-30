package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const releasesURL = "https://api.github.com/repos/londek/ipadecrypt/releases/latest"

type Release struct {
	Tag         string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
}

func fetchLatest(ctx context.Context, currentVersion string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "ipadecrypt/"+currentVersion)

	c := &http.Client{Timeout: 5 * time.Second}

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases: status %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	if rel.Draft || rel.Prerelease {
		return nil, fmt.Errorf("latest release is draft/prerelease")
	}

	if rel.Tag == "" {
		return nil, fmt.Errorf("empty tag_name")
	}

	return &rel, nil
}
