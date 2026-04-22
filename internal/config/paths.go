package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	Root     string
	cacheDir string
}

func NewPaths(root string) (*Paths, error) {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("home dir: %w", err)
		}
		root = filepath.Join(home, ".ipadecrypt")
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", root, err)
	}

	return &Paths{Root: root}, nil
}

func (p *Paths) ConfigPath() string {
	return filepath.Join(p.Root, "config.json")
}

func (p *Paths) CacheDir() (string, error) {
	if p.cacheDir != "" {
		return p.ensure(p.cacheDir)
	}

	return p.ensure(filepath.Join(p.Root, "cache"))
}

func (p *Paths) CachedEncryptedIPA(bundleID string, version string) (string, error) {
	dir, err := p.CacheDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, fmt.Sprintf("%s_%s.ipa", bundleID, version)), nil
}

func (p *Paths) ensure(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}

	return dir, nil
}
