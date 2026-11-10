package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCredentialsFileReadsAndDeletes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	data := []byte(`{"email":"user@example.com","password":"s3cret","auth_code":"123456"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	email, password, code, err := loadCredentialsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if email != "user@example.com" || password != "s3cret" || code != "123456" {
		t.Fatalf("got %q %q %q", email, password, code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("credentials file should be deleted: %v", err)
	}
}

func TestLoadCredentialsFileEmptyPath(t *testing.T) {
	email, password, code, err := loadCredentialsFile("")
	if err != nil || email != "" || password != "" || code != "" {
		t.Fatalf("empty path should no-op, got %q %q %q %v", email, password, code, err)
	}
}

func TestMergeCredentialsPrefersFlagsThenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(path, []byte(`{"email":"file@example.com","password":"filepass","auth_code":"000000"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	email, password, code, err := mergeCredentials("flag@example.com", "", "111111", path)
	if err != nil {
		t.Fatal(err)
	}
	if email != "flag@example.com" {
		t.Fatalf("email %q", email)
	}
	if password != "filepass" {
		t.Fatalf("password should come from file, got %q", password)
	}
	if code != "111111" {
		t.Fatalf("auth code %q", code)
	}
}
