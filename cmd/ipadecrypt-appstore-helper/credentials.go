package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const maxCredentialJSON = 1 << 20

type appleCredentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	AuthCode string `json:"auth_code"`
}

func zeroBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

func zeroString(value *string) {
	if value == nil {
		return
	}
	// strings are immutable; replace with a zeroed copy then drop.
	n := len(*value)
	if n == 0 {
		return
	}
	buf := make([]byte, n)
	copy(buf, *value)
	zeroBytes(buf)
	*value = ""
}

// readCredentialsFromStdin reads one JSON message from stdin and closes it.
// An empty stdin is a no-op so commands that do not need credentials can
// inherit a closed pipe. Nothing is written to disk or argv.
func readCredentialsFromStdin(r io.Reader, closer io.Closer) (email, password, authCode string, err error) {
	if closer != nil {
		defer closer.Close()
	}
	if r == nil {
		return "", "", "", nil
	}

	data, err := io.ReadAll(io.LimitReader(r, maxCredentialJSON+1))
	if err != nil {
		zeroBytes(data)
		return "", "", "", fmt.Errorf("read credentials from stdin: %w", err)
	}
	defer zeroBytes(data)

	if len(data) > maxCredentialJSON {
		return "", "", "", errors.New("credentials message is too large")
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return "", "", "", nil
	}

	var creds appleCredentials
	if err := json.Unmarshal(trimmed, &creds); err != nil {
		return "", "", "", fmt.Errorf("parse credentials from stdin: %w", err)
	}

	email = strings.TrimSpace(creds.Email)
	password = strings.TrimSpace(creds.Password)
	authCode = strings.TrimSpace(creds.AuthCode)
	zeroString(&creds.Email)
	zeroString(&creds.Password)
	zeroString(&creds.AuthCode)
	if email == "" && password == "" && authCode == "" {
		return "", "", "", errors.New("credentials message is empty")
	}
	return email, password, authCode, nil
}

func credentialsNeeded(authOnly, prefetchAssets, authStatus, hasTarget, verifyIPA, decryptHelper bool) bool {
	if prefetchAssets || authStatus || verifyIPA || decryptHelper {
		return false
	}
	return authOnly || hasTarget
}
