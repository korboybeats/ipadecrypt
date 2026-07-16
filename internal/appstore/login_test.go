package appstore

import (
	"io"
	"net/http"
	"strings"
	"testing"

	cookiejar "github.com/juju/persistent-cookiejar"
	"howett.net/plist"
)

func TestLoginEndpointDiscoveryDoesNotConsumeLogicalAttempt(t *testing.T) {
	var attempts []string
	authRequests := 0

	client := &Client{
		jar: &cookiejar.Jar{},
		http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				body := `{ authenticateAccount = "https://buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/authenticate"; }`
				return testHTTPResponse(http.StatusOK, body), nil
			}

			authRequests++
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}
			var request struct {
				Attempt string `plist:"attempt"`
			}
			if _, err := plist.Unmarshal(body, &request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			attempts = append(attempts, request.Attempt)

			switch authRequests {
			case 1:
				return testHTTPResponse(http.StatusOK, `redirect=https:\/\/auth.itunes.apple.com\/auth\/v1\/native`), nil
			case 2:
				return testHTTPResponse(http.StatusOK, `{ failureType = "-5000"; }`), nil
			default:
				res := testHTTPResponse(http.StatusOK, `{ dsPersonId = "123"; passwordToken = "token"; }`)
				res.Header.Set(hdrStoreFront, "143441-1,29")
				return res, nil
			}
		})},
	}

	if _, err := client.Login("test@example.com", "password", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}
	want := []string{"1", "1", "2"}
	if strings.Join(attempts, ",") != strings.Join(want, ",") {
		t.Fatalf("attempts = %v, want %v", attempts, want)
	}
}

func testHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
