package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/mod/semver"
)

const releasesURL = "https://api.github.com/repos/korboybeats/ipadecrypt/releases?per_page=30"

type Release struct {
	Tag         string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	Assets      []Asset   `json:"assets"`
}

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func (r *Release) FindAsset(name string) (Asset, bool) {
	if r == nil {
		return Asset{}, false
	}
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return Asset{}, false
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

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	rel, err := selectLatestRelease(releases)
	if err != nil {
		return nil, err
	}

	return rel, nil
}

func selectLatestRelease(releases []Release) (*Release, error) {
	var latest *Release
	for i := range releases {
		rel := releases[i]
		if rel.Tag == "" || rel.Draft || rel.Prerelease || !semver.IsValid(rel.Tag) {
			continue
		}

		if latest == nil || semver.Compare(rel.Tag, latest.Tag) > 0 {
			latest = &releases[i]
		}
	}

	if latest == nil {
		return nil, fmt.Errorf("no usable releases found")
	}

	if latest.Draft || latest.Prerelease {
		return nil, fmt.Errorf("latest release is draft/prerelease")
	}

	if latest.Tag == "" {
		return nil, fmt.Errorf("empty tag_name")
	}
	if len(latest.Assets) == 0 {
		return nil, fmt.Errorf("release %s has no assets", latest.Tag)
	}

	return latest, nil
}
