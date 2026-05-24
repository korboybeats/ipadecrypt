package appstore

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"howett.net/plist"
)

// httpRangeReader is the io.ReaderAt that the archive/zip stdlib drives
// when we hand it a URL instead of a local file. Each ReadAt fires one
// HTTP GET with a Range header  for an IPA the std reader makes 2-3
// calls total (last 64KB for EOCD + central dir, then the Info.plist
// entry's local header + compressed bytes).
type httpRangeReader struct {
	url    string
	client *http.Client
}

func (r *httpRangeReader) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	req, err := http.NewRequest(http.MethodGet, r.url, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+int64(len(p))-1))

	res, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusPartialContent && res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("range fetch %s status %d", r.url, res.StatusCode)
	}

	n, err := io.ReadFull(res.Body, p)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		// Hit end-of-resource mid-read; zip.Reader treats EOF as terminal.
		err = io.EOF
	}

	return n, err
}

// FetchMinimumOSVersion returns the value of MinimumOSVersion from the
// Payload/<App>.app/Info.plist of the IPA at meta.URL. No full download:
// archive/zip's central directory pass is driven through HTTP Range, so
// the CDN only serves the bytes we actually need (tens to low-hundreds
// of KB instead of a multi-GB IPA).
//
// Returns "" with no error when MinimumOSVersion isn't present in the
// plist (very old apps); errors only fire for HTTP / zip / plist parse
// failures so callers can render the column as blank without surfacing
// a noisy error to the TUI.
func (c *Client) FetchMinimumOSVersion(meta VersionMetadata) (string, error) {
	if meta.URL == "" {
		return "", errors.New("fetch minOS: no URL on metadata")
	}

	size := meta.FileSize
	if size <= 0 {
		req, err := http.NewRequest(http.MethodHead, meta.URL, nil)
		if err != nil {
			return "", err
		}

		res, err := c.http.Do(req)
		if err != nil {
			return "", fmt.Errorf("HEAD %s: %w", meta.URL, err)
		}

		res.Body.Close()

		size = res.ContentLength
		if size <= 0 {
			return "", errors.New("fetch minOS: CDN didn't report size")
		}
	}

	zr, err := zip.NewReader(&httpRangeReader{url: meta.URL, client: c.http}, size)
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}

	for _, f := range zr.File {
		// Want exactly Payload/<App>.app/Info.plist  not nested Frameworks
		// or appex Info.plists.
		if !strings.HasPrefix(f.Name, "Payload/") {
			continue
		}

		parts := strings.SplitN(strings.TrimPrefix(f.Name, "Payload/"), "/", 3)
		if len(parts) != 2 || parts[1] != "Info.plist" {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open Info.plist: %w", err)
		}

		data, err := io.ReadAll(rc)
		rc.Close()

		if err != nil {
			return "", fmt.Errorf("read Info.plist: %w", err)
		}

		var info struct {
			MinimumOSVersion string `plist:"MinimumOSVersion"`
		}
		if _, err := plist.Unmarshal(data, &info); err != nil {
			return "", fmt.Errorf("parse Info.plist: %w", err)
		}

		return info.MinimumOSVersion, nil
	}

	return "", errors.New("no Info.plist in IPA")
}
