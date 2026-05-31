package appstore

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestNewCreatesCookieParentDirectory(t *testing.T) {
	cookiesFile := filepath.Join(t.TempDir(), "missing", "cookies")

	client, err := New(cookiesFile)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if client == nil {
		t.Fatal("New returned nil client")
	}

	if _, err := os.Stat(filepath.Dir(cookiesFile)); err != nil {
		t.Fatalf("cookie parent directory was not created: %v", err)
	}

	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       http.NoBody,
		}, nil
	})}

	if _, err := client.send(http.MethodGet, "https://example.test", nil, nil, formatXML, nil); err != nil {
		t.Fatalf("send returned error: %v", err)
	}
	if _, err := os.Stat(cookiesFile); err != nil {
		t.Fatalf("cookies file was not saved: %v", err)
	}
}
