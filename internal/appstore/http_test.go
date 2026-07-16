package appstore

import (
	"errors"
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
	if got := err.Error(); !strings.Contains(got, "apple auth is rate-limited (HTTP 429)") {
		t.Fatalf("error = %q, want rate-limit message", got)
	}
}

func TestSendReturnsRateLimitErrorWithoutDecodeTarget(t *testing.T) {
	client := &Client{
		jar: &cookiejar.Jar{},
		http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("Rate limit has been exceeded")),
			}, nil
		})},
	}

	res, err := client.send(http.MethodPost, "https://example.test/auth", nil, nil, formatXML, nil)
	if err == nil || !strings.Contains(err.Error(), "apple auth is rate-limited (HTTP 429)") {
		t.Fatalf("error = %v, want rate-limit message", err)
	}
	if res == nil || res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("response = %#v, want HTTP 429", res)
	}
}

func TestSendPreservesDiscoveryURLAndRedactsDecodeError(t *testing.T) {
	const secret = "supersecret"
	client := &Client{
		jar: &cookiejar.Jar{},
		http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := "passwordToken=" + secret + " https://auth.itunes.apple.com/auth/v1/native"
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})},
	}

	var out loginResult
	_, err := client.send(http.MethodPost, "https://example.test/auth", nil, nil, formatXML, &out)
	var decodeErr *ResponseDecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *ResponseDecodeError", err)
	}
	if strings.Contains(decodeErr.Body, secret) {
		t.Fatalf("decode body leaked secret: %q", decodeErr.Body)
	}
	want := "https://auth.itunes.apple.com/auth/v1/native/fast/"
	if got := authEndpointFromResponseError(err); got != want {
		t.Fatalf("discovered endpoint = %q, want %q", got, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
