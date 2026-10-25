package appstore

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	cookiejar "github.com/juju/persistent-cookiejar"
	apphttp "github.com/londek/ipadecrypt/internal/apphttp"
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
		executorFor: fakeExecutorFor(func(op string, req apphttp.Request, raw []byte) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("Rate limit has been exceeded for: mzauth|global|all")),
			}, nil
		}),
	}

	var out loginResult
	_, err := client.send("authenticate", http.MethodPost, "https://example.test/auth", nil, nil, nil, formatXML, &out)
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
		executorFor: fakeExecutorFor(func(op string, req apphttp.Request, raw []byte) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("Rate limit has been exceeded")),
			}, nil
		}),
	}

	res, err := client.send("authenticate", http.MethodPost, "https://example.test/auth", nil, nil, nil, formatXML, nil)
	if err == nil || !strings.Contains(err.Error(), "apple auth is rate-limited (HTTP 429)") {
		t.Fatalf("error = %v, want rate-limit message", err)
	}
	if res == nil || res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("response = %#v, want HTTP 429", res)
	}
}

func TestSendRedactsDecodeErrorAndKeepsURLs(t *testing.T) {
	const secret = "supersecret"
	client := &Client{
		jar: &cookiejar.Jar{},
		executorFor: fakeExecutorFor(func(op string, req apphttp.Request, raw []byte) (*http.Response, error) {
			body := "passwordToken=" + secret + " https://auth.itunes.apple.com/auth/v1/native"
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	}

	var out loginResult
	_, err := client.send("authenticate", http.MethodPost, "https://example.test/auth", nil, nil, nil, formatXML, &out)
	var decodeErr *ResponseDecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *ResponseDecodeError", err)
	}
	if strings.Contains(decodeErr.Body, secret) {
		t.Fatalf("decode body leaked secret: %q", decodeErr.Body)
	}
	want := "https://auth.itunes.apple.com/auth/v1/native"
	if len(decodeErr.URLs) != 1 || decodeErr.URLs[0] != want {
		t.Fatalf("decode URLs = %v, want [%s]", decodeErr.URLs, want)
	}
}

func TestResponsePreviewRedactsEverySensitiveOccurrence(t *testing.T) {
	inputs := []string{
		`passwordToken = "first"; passwordToken = "second"; status = "failed";`,
		`{"passwordToken":"first","nested":{"passwordToken":"second"}}`,
		`<key>passwordToken</key><string>first</string><key>passwordToken</key><string>second</string>`,
	}

	for _, input := range inputs {
		got := responsePreview([]byte(input))
		if strings.Contains(got, "first") || strings.Contains(got, "second") {
			t.Fatalf("responsePreview leaked a secret: %q", got)
		}
	}
}

// fakeExecutor stands in for the emulated AppleMediaServices backend so
// send's response handling can be tested without the Store runtime.
type fakeExecutor struct {
	op   string
	send func(op string, req apphttp.Request, raw []byte) (*http.Response, error)
}

func (f fakeExecutor) Send(req apphttp.Request, raw []byte) (*http.Response, error) {
	return f.send(f.op, req, raw)
}

func (f fakeExecutor) Do(req *http.Request) (*http.Response, error) {
	return nil, errors.New("fakeExecutor: Do not supported")
}

func fakeExecutorFor(send func(op string, req apphttp.Request, raw []byte) (*http.Response, error)) func(string) apphttp.Executor {
	return func(op string) apphttp.Executor {
		return fakeExecutor{op: op, send: send}
	}
}
