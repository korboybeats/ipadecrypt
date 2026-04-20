package appstore

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"howett.net/plist"
)

type DownloadOutput struct {
	DestinationPath string
	Sinfs           []Sinf
}

type downloadItem struct {
	URL      string                 `plist:"URL,omitempty"`
	Sinfs    []Sinf                 `plist:"sinfs,omitempty"`
	Metadata map[string]interface{} `plist:"metadata,omitempty"`
}

type downloadResult struct {
	FailureType     string         `plist:"failureType,omitempty"`
	CustomerMessage string         `plist:"customerMessage,omitempty"`
	Items           []downloadItem `plist:"songList,omitempty"`
}

// Download fetches the IPA for app, writes it to outPath (directory or full
// file path), injects iTunesMetadata.plist and replicates sinfs.
//
// On ErrPasswordTokenExpired the caller must re-Login and retry.
// On ErrLicenseRequired the caller must Purchase and retry.
func (c *Client) Download(acc Account, app App, outPath, externalVersionID string) (DownloadOutput, error) {
	g, err := guid()
	if err != nil {
		return DownloadOutput{}, err
	}

	podPrefix := ""
	if acc.Pod != "" {
		podPrefix = "p" + acc.Pod + "-"
	}

	url := fmt.Sprintf("https://%s%s%s?guid=%s", podPrefix, storeDomain, downloadPath, g)

	payload := map[string]any{
		"creditDisplay": "",
		"guid":          g,
		"salableAdamId": app.ID,
	}
	if externalVersionID != "" {
		payload["externalVersionId"] = externalVersionID
	}

	body, err := plistBody(payload)
	if err != nil {
		return DownloadOutput{}, err
	}

	headers := map[string]string{
		"Content-Type": "application/x-apple-plist",
		"iCloud-DSID":  acc.DirectoryServicesID,
		"X-Dsid":       acc.DirectoryServicesID,
	}

	var out downloadResult
	if _, err := c.send(http.MethodPost, url, headers, body, formatXML, &out); err != nil {
		return DownloadOutput{}, fmt.Errorf("download: %w", err)
	}

	switch {
	case out.FailureType == failurePasswordTokenExpired,
		out.FailureType == failureSignInRequired,
		out.FailureType == failureDeviceVerificationFailed,
		out.FailureType == failureLicenseAlreadyExists:
		return DownloadOutput{}, ErrPasswordTokenExpired
	case out.FailureType == failureLicenseNotFound:
		return DownloadOutput{}, ErrLicenseRequired
	case out.FailureType != "" && out.CustomerMessage != "":
		return DownloadOutput{}, errors.New(out.CustomerMessage)
	case out.FailureType != "":
		return DownloadOutput{}, fmt.Errorf("download: %s", out.FailureType)
	case len(out.Items) == 0:
		return DownloadOutput{}, errors.New("download: empty songList")
	}

	item := out.Items[0]

	version := "unknown"
	if v, ok := item.Metadata["bundleShortVersionString"]; ok {
		version = fmt.Sprintf("%v", v)
	}

	dst, err := resolveDestination(app, version, outPath)
	if err != nil {
		return DownloadOutput{}, err
	}

	tmp := dst + ".tmp"
	if err := fetchToFile(c.http, item.URL, tmp); err != nil {
		return DownloadOutput{}, err
	}

	if err := applyPatches(tmp, dst, item, acc); err != nil {
		return DownloadOutput{}, err
	}

	if err := os.Remove(tmp); err != nil {
		return DownloadOutput{}, fmt.Errorf("remove tmp: %w", err)
	}

	return DownloadOutput{DestinationPath: dst, Sinfs: item.Sinfs}, nil
}

func fetchToFile(hc *http.Client, url, dst string) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", dst, err)
	}
	defer f.Close()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	if stat, err := f.Stat(); err == nil && stat.Size() > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", stat.Size()))
	}

	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	defer res.Body.Close()

	if _, err := io.Copy(f, res.Body); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}

	return nil
}

func resolveDestination(app App, version, path string) (string, error) {
	file := fileName(app, version)

	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(cwd, file), nil
	}

	info, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if info != nil && info.IsDir() {
		return filepath.Join(path, file), nil
	}

	return path, nil
}

func fileName(app App, version string) string {
	var parts []string
	if app.BundleID != "" {
		parts = append(parts, app.BundleID)
	}
	if app.ID != 0 {
		parts = append(parts, strconv.FormatInt(app.ID, 10))
	}
	if version != "" {
		parts = append(parts, version)
	}
	return strings.Join(parts, "_") + ".ipa"
}

// applyPatches rebuilds src into dst with iTunesMetadata.plist injected and
// sinfs replicated into either manifest-listed paths or the SC_Info fallback.
func applyPatches(src, dst string, item downloadItem, acc Account) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer zr.Close()

	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", dst, err)
	}
	defer df.Close()

	zw := zip.NewWriter(df)
	defer zw.Close()

	for _, f := range zr.File {
		if err := copyZipEntry(f, zw); err != nil {
			return err
		}
	}

	if err := writeMetadataEntry(zw, item.Metadata, acc); err != nil {
		return err
	}

	bundleName, err := readBundleName(zr)
	if err != nil {
		return err
	}

	manifest, err := readManifest(zr)
	if err != nil {
		return err
	}

	if manifest != nil {
		if len(item.Sinfs) != len(manifest.SinfPaths) {
			return fmt.Errorf("sinf count mismatch: have %d, manifest wants %d", len(item.Sinfs), len(manifest.SinfPaths))
		}
		for i, p := range manifest.SinfPaths {
			entry := fmt.Sprintf("Payload/%s.app/%s", bundleName, p)
			if err := writeEntry(zw, entry, item.Sinfs[i].Data); err != nil {
				return err
			}
		}
		return nil
	}

	info, err := readInfo(zr)
	if err != nil {
		return err
	}
	if info == nil {
		return errors.New("no Info.plist in package")
	}
	if len(item.Sinfs) == 0 {
		return errors.New("no sinfs in download response")
	}

	entry := fmt.Sprintf("Payload/%s.app/SC_Info/%s.sinf", bundleName, info.BundleExecutable)
	return writeEntry(zw, entry, item.Sinfs[0].Data)
}

func copyZipEntry(f *zip.File, zw *zip.Writer) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	hdr := f.FileHeader
	w, err := zw.CreateHeader(&hdr)
	if err != nil {
		return err
	}

	_, err = io.Copy(w, rc)
	return err
}

func writeEntry(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func writeMetadataEntry(zw *zip.Writer, metadata map[string]interface{}, acc Account) error {
	metadata["apple-id"] = acc.Email
	metadata["userName"] = acc.Email

	data, err := plist.Marshal(metadata, plist.BinaryFormat)
	if err != nil {
		return fmt.Errorf("marshal iTunesMetadata: %w", err)
	}

	return writeEntry(zw, "iTunesMetadata.plist", data)
}

type pkgManifest struct {
	SinfPaths []string `plist:"SinfPaths,omitempty"`
}

type pkgInfo struct {
	BundleExecutable string `plist:"CFBundleExecutable,omitempty"`
}

func readManifest(zr *zip.ReadCloser) (*pkgManifest, error) {
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".app/SC_Info/Manifest.plist") {
			continue
		}
		data, err := readZipFile(f)
		if err != nil {
			return nil, err
		}
		var m pkgManifest
		if _, err := plist.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parse Manifest.plist: %w", err)
		}
		return &m, nil
	}
	return nil, nil
}

func readInfo(zr *zip.ReadCloser) (*pkgInfo, error) {
	for _, f := range zr.File {
		if !strings.Contains(f.Name, ".app/Info.plist") || strings.Contains(f.Name, "/Watch/") {
			continue
		}
		data, err := readZipFile(f)
		if err != nil {
			return nil, err
		}
		var i pkgInfo
		if _, err := plist.Unmarshal(data, &i); err != nil {
			return nil, fmt.Errorf("parse Info.plist: %w", err)
		}
		return &i, nil
	}
	return nil, nil
}

func readBundleName(zr *zip.ReadCloser) (string, error) {
	for _, f := range zr.File {
		if strings.Contains(f.Name, ".app/Info.plist") && !strings.Contains(f.Name, "/Watch/") {
			return filepath.Base(strings.TrimSuffix(f.Name, ".app/Info.plist")), nil
		}
	}
	return "", errors.New("no .app/Info.plist in package")
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
