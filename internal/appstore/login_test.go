package appstore

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInterpretLoginReturnsTypedInvalidCredentials(t *testing.T) {
	res := testHTTPResponse(http.StatusOK, "")
	out := &loginResult{FailureType: failureInvalidCredentials, CustomerMessage: "Incorrect Apple ID or password"}

	retry, err := interpretLogin(res, out, 2, "")
	if retry {
		t.Fatal("invalid credentials should not retry after the first attempt")
	}
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error = %v, want ErrInvalidCredentials", err)
	}
}

func TestInterpretLoginRetriesFirstInvalidCredentialsOnSameEndpoint(t *testing.T) {
	res := testHTTPResponse(http.StatusOK, "")
	out := &loginResult{FailureType: failureInvalidCredentials}

	retry, err := interpretLogin(res, out, 1, "")
	if err != nil {
		t.Fatalf("interpretLogin: %v", err)
	}
	if !retry {
		t.Fatal("first invalid-credentials response should retry")
	}
}

func TestInterpretLoginIgnoresDialogWhenTokenPresent(t *testing.T) {
	res := testHTTPResponse(http.StatusOK, "")
	out := &loginResult{
		PasswordToken: "tok",
		Dialog: loginDialog{
			Message:     "Welcome",
			Explanation: "informational",
		},
	}

	retry, err := interpretLogin(res, out, 1, "")
	if err != nil {
		t.Fatalf("interpretLogin: %v", err)
	}
	if retry {
		t.Fatal("token+dialog should succeed without retry")
	}
}

func testHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
