package appstore

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	apphttp "github.com/londek/ipadecrypt/internal/apphttp"
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

	client.executorFor = fakeExecutorFor(func(op string, req apphttp.Request, raw []byte) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       http.NoBody,
		}, nil
	})

	if _, err := client.send("request", http.MethodGet, "https://example.test", nil, nil, nil, formatXML, nil); err != nil {
		t.Fatalf("send returned error: %v", err)
	}
	if _, err := os.Stat(cookiesFile); err != nil {
		t.Fatalf("cookies file was not saved: %v", err)
	}
}
