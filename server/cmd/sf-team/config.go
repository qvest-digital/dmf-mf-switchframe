package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type diskConfig struct {
	Token string `json:"token"`
}

func configPath() (string, error) {
	home, err := teamHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "config.json"), nil
}

func loadOrInitConfig() (diskConfig, error) {
	path, err := configPath()
	if err != nil {
		return diskConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var c diskConfig
		if jerr := json.Unmarshal(data, &c); jerr != nil {
			return diskConfig{}, fmt.Errorf("parse config.json: %w", jerr)
		}
		return c, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return diskConfig{}, err
	}

	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return diskConfig{}, err
	}
	c := diskConfig{Token: hex.EncodeToString(tokenBytes)}
	out, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return diskConfig{}, err
	}
	return c, nil
}
