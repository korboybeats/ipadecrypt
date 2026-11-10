package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

type closeBuffer struct {
	*bytes.Buffer
	closed bool
}

func (c *closeBuffer) Close() error {
	c.closed = true
	return nil
}

func TestReadCredentialsFromStdin(t *testing.T) {
	src := &closeBuffer{Buffer: bytes.NewBufferString(`{"email":"user@example.com","password":"s3cret","auth_code":"123456"}`)}
	email, password, code, err := readCredentialsFromStdin(src, src)
	if err != nil {
		t.Fatal(err)
	}
	if email != "user@example.com" || password != "s3cret" || code != "123456" {
		t.Fatalf("got %q %q %q", email, password, code)
	}
	if !src.closed {
		t.Fatal("stdin should be closed after the message is read")
	}
}

func TestReadCredentialsFromStdinEmptyIsNoOp(t *testing.T) {
	src := &closeBuffer{Buffer: bytes.NewBuffer(nil)}
	email, password, code, err := readCredentialsFromStdin(src, src)
	if err != nil || email != "" || password != "" || code != "" {
		t.Fatalf("empty stdin should no-op, got %q %q %q %v", email, password, code, err)
	}
	if !src.closed {
		t.Fatal("stdin should still be closed")
	}
}

func TestReadCredentialsFromStdinWhitespaceOnly(t *testing.T) {
	src := &closeBuffer{Buffer: bytes.NewBufferString("  \n")}
	email, password, code, err := readCredentialsFromStdin(src, src)
	if err != nil || email != "" || password != "" || code != "" {
		t.Fatalf("whitespace stdin should no-op, got %v", err)
	}
}

func TestReadCredentialsFromStdinRejectsInvalidJSON(t *testing.T) {
	src := &closeBuffer{Buffer: bytes.NewBufferString(`not-json`)}
	_, _, _, err := readCredentialsFromStdin(src, src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if strings.Contains(err.Error(), "not-json") && strings.Contains(err.Error(), "s3cret") {
		t.Fatal("error should not echo credential material")
	}
}

func TestReadCredentialsFromStdinRejectsOversize(t *testing.T) {
	body := `{"email":"` + strings.Repeat("a", maxCredentialJSON) + `"}`
	_, _, _, err := readCredentialsFromStdin(bytes.NewBufferString(body), io.NopCloser(nil))
	if err == nil {
		t.Fatal("expected oversize error")
	}
}

func TestCredentialsNeeded(t *testing.T) {
	if !credentialsNeeded(true, false, false, false, false, false) {
		t.Fatal("auth-only should read stdin")
	}
	if !credentialsNeeded(false, false, false, true, false, false) {
		t.Fatal("store target should read stdin")
	}
	if credentialsNeeded(false, true, false, false, false, false) {
		t.Fatal("prefetch must not block on stdin")
	}
	if credentialsNeeded(false, false, true, false, false, false) {
		t.Fatal("auth-status must not block on stdin")
	}
	if credentialsNeeded(false, false, false, false, true, false) {
		t.Fatal("verify-ipa must not block on stdin")
	}
	if credentialsNeeded(false, false, false, false, false, true) {
		t.Fatal("decrypt-helper must not block on stdin")
	}
}

func TestZeroBytesClearsBuffer(t *testing.T) {
	data := []byte("s3cret")
	zeroBytes(data)
	for _, b := range data {
		if b != 0 {
			t.Fatal("buffer was not zeroed")
		}
	}
}
