package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type appleCredentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	AuthCode string `json:"auth_code"`
}

// loadCredentialsFile reads a 0600 JSON credentials file and removes it.
// Password and auth-code must never travel on argv.
func loadCredentialsFile(path string) (email, password, authCode string, err error) {
	if strings.TrimSpace(path) == "" {
		return "", "", "", nil
	}

	defer func() { _ = os.Remove(path) }()

	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", fmt.Errorf("read credentials file: %w", err)
	}

	var creds appleCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", "", "", fmt.Errorf("parse credentials file: %w", err)
	}

	creds.Email = strings.TrimSpace(creds.Email)
	creds.Password = strings.TrimSpace(creds.Password)
	creds.AuthCode = strings.TrimSpace(creds.AuthCode)
	if creds.Email == "" && creds.Password == "" && creds.AuthCode == "" {
		return "", "", "", errors.New("credentials file is empty")
	}

	return creds.Email, creds.Password, creds.AuthCode, nil
}

func mergeCredentials(email, password, authCode, filePath string) (string, string, string, error) {
	fileEmail, filePassword, fileAuthCode, err := loadCredentialsFile(filePath)
	if err != nil {
		return "", "", "", err
	}
	if email == "" {
		email = fileEmail
	}
	if password == "" {
		password = filePassword
	}
	if authCode == "" {
		authCode = fileAuthCode
	}
	return email, password, authCode, nil
}
