package appstore

import (
	"io"
	"net/http"
	"strings"
	"testing"

	cookiejar "github.com/juju/persistent-cookiejar"
	"howett.net/plist"
)

func TestNormalizePlistWrapsBareOpenStepDictionary(t *testing.T) {
	body := []byte(`failureType = 2042;
customerMessage = "MZFinance.BadLogin.Configurator_message";`)

	var out struct {
		FailureType     string `plist:"failureType"`
		CustomerMessage string `plist:"customerMessage"`
	}
	if _, err := plist.Unmarshal(normalizePlist(body), &out); err != nil {
		t.Fatalf("unmarshal normalized text plist: %v", err)
	}
	if out.FailureType != "2042" {
		t.Fatalf("FailureType = %q, want 2042", out.FailureType)
	}
	if out.CustomerMessage != custMsgBadLogin {
		t.Fatalf("CustomerMessage = %q, want %q", out.CustomerMessage, custMsgBadLogin)
	}
}

func TestSendReturnsClearRateLimitError(t *testing.T) {
	client := &Client{
		jar: &cookiejar.Jar{},
		http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("Rate limit has been exceeded for: mzauth|global|all")),
			}, nil
		})},
	}

	var out loginResult
	_, err := client.send(http.MethodPost, "https://example.test/auth", nil, nil, formatXML, &out)
	if err == nil {
		t.Fatal("send returned nil error")
	}
	if got := err.Error(); !strings.Contains(got, "Apple auth is rate-limited (HTTP 429)") {
		t.Fatalf("error = %q, want rate-limit message", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
