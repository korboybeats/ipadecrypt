package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/londek/ipadecrypt/internal/appstore"
)

const SchemaVersion = 2

type Config struct {
	Version     int         `json:"version"`
	Apple       Apple       `json:"apple"`
	Device      Device      `json:"device"`
	Output      Output      `json:"output,omitempty"`
	Versions    Versions    `json:"versions,omitempty"`
	UpdateCheck UpdateCheck `json:"updateCheck,omitzero"`

	path string
}

const (
	OutputKeepDesktop = "desktop"
	OutputKeepDevice  = "device"
	OutputKeepBoth    = "both"
)

type Output struct {
	Keep string `json:"keep,omitempty"`
}

type UpdateCheck struct {
	CheckedAt  time.Time `json:"checkedAt,omitzero"`
	LatestTag  string    `json:"latestTag,omitempty"`
	HTMLURL    string    `json:"htmlUrl,omitempty"`
	NotifiedAt time.Time `json:"notifiedAt,omitzero"`
}

type Versions struct {
	WarningAccepted bool `json:"warningAccepted,omitempty"`
}

type Apple struct {
	Email                       string `json:"email,omitempty"`
	Password                    string `json:"password,omitempty"`
	PasswordToken               string `json:"passwordToken,omitempty"`
	DirectoryServicesIdentifier string `json:"directoryServicesIdentifier,omitempty"`
	StoreFront                  string `json:"storeFront,omitempty"`
	Pod                         string `json:"pod,omitempty"`
}

func (a Apple) Account() *appstore.Account {
	return &appstore.Account{
		Email:               a.Email,
		Password:            a.Password,
		PasswordToken:       a.PasswordToken,
		DirectoryServicesID: a.DirectoryServicesIdentifier,
		StoreFront:          a.StoreFront,
		Pod:                 a.Pod,
	}
}

func (a *Apple) SetAccount(acc *appstore.Account) {
	a.Email = acc.Email
	a.Password = acc.Password
	a.PasswordToken = acc.PasswordToken
	a.DirectoryServicesIdentifier = acc.DirectoryServicesID
	a.StoreFront = acc.StoreFront
	a.Pod = acc.Pod
}

type Device struct {
	Host             string     `json:"host,omitempty"`
	Port             int        `json:"port,omitempty"`
	User             string     `json:"user,omitempty"`
	Auth             DeviceAuth `json:"auth,omitempty"`
	KnownHostsPath   string     `json:"knownHostsPath,omitempty"`
	AcceptNewHostKey bool       `json:"acceptNewHostKey,omitempty"`
}

type DeviceAuth struct {
	Kind          string `json:"kind,omitempty"`
	Password      string `json:"password,omitempty"`
	KeyPath       string `json:"keyPath,omitempty"`
	KeyPassphrase string `json:"keyPassphrase,omitempty"`
}

func New(path string) *Config {
	return &Config{Version: SchemaVersion, path: path}
}

func NormalizeOutputKeep(value string) (string, error) {
	switch value {
	case "", OutputKeepBoth:
		return OutputKeepBoth, nil
	case OutputKeepDesktop:
		return OutputKeepDesktop, nil
	case OutputKeepDevice:
		return OutputKeepDevice, nil
	case "local", "pc", "computer":
		return OutputKeepDesktop, nil
	case "phone":
		return OutputKeepDevice, nil
	default:
		return "", fmt.Errorf("output keep must be one of: desktop, device, both")
	}
}

func (c *Config) OutputKeep() string {
	keep, err := NormalizeOutputKeep(c.Output.Keep)
	if err != nil {
		return OutputKeepBoth
	}

	return keep
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{path: path}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if cfg.Version == 0 {
		cfg.Version = SchemaVersion
	}

	if cfg.Version < 2 {
		migrateAppleV1ToV2(data, &cfg.Apple)
		cfg.Version = 2

		// Persist the migrated shape so subsequent loads are clean and
		// the old apple.account.* nested object stops shadowing the new
		// flat fields if anyone hand-edits the file.
		if err := cfg.Save(); err != nil {
			return nil, fmt.Errorf("save migrated config: %w", err)
		}
	}

	return cfg, nil
}

// migrateAppleV1ToV2 lifts the v1 apple.account.* nested object into the
// flat v2 apple.* fields. Top-level apple.email/apple.password already
// share JSON keys with the v2 schema, so they're carried over by the
// initial Unmarshal - this only rescues the account-nested credentials.
func migrateAppleV1ToV2(data []byte, apple *Apple) {
	var raw struct {
		Apple struct {
			Account struct {
				Email               string `json:"email"`
				PasswordToken       string `json:"passwordToken"`
				DirectoryServicesID string `json:"directoryServicesIdentifier"`
				StoreFront          string `json:"storeFront"`
				Password            string `json:"password"`
				Pod                 string `json:"pod"`
			} `json:"account"`
		} `json:"apple"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}

	a := raw.Apple.Account

	if apple.Email == "" {
		apple.Email = a.Email
	}

	if apple.Password == "" {
		apple.Password = a.Password
	}

	if apple.PasswordToken == "" {
		apple.PasswordToken = a.PasswordToken
	}

	if apple.DirectoryServicesIdentifier == "" {
		apple.DirectoryServicesIdentifier = a.DirectoryServicesID
	}

	if apple.StoreFront == "" {
		apple.StoreFront = a.StoreFront
	}

	if apple.Pod == "" {
		apple.Pod = a.Pod
	}
}

func (c *Config) Save() error {
	if c.path == "" {
		return errors.New("config: no path")
	}

	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	tmp := c.path + ".tmp"

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", tmp, err)
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", tmp, err)
	}

	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", tmp, err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, c.path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}
