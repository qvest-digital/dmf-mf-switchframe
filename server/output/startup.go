package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StartupConfig lists the outputs a process starts with, so a deployment can
// declare them instead of creating them over the API after every start: the
// manager keeps destinations and recordings in memory only.
//
// It is read from the file --outputs-config names:
//
//	{
//	  "destinations": [
//	    {"type": "srt-caller", "address": "203.0.113.5", "port": 9000,
//	     "streamID": "program", "encryption": "aes-128",
//	     "passphraseFile": "/run/secrets/srt-out/passphrase"},
//	    {"type": "rtmp", "url": "rtmp://ingest.example.com/live/key"}
//	  ],
//	  "recording": {"outputDir": "/recordings", "rotateAfterMins": 30}
//	}
//
// A destination takes the fields POST /api/output/destinations takes. Every
// destination listed is started before the API comes up; an srt-caller dials
// once with its connect timeout first and keeps reconnecting after, so an
// unreachable peer delays startup by that timeout rather than failing it.
type StartupConfig struct {
	Destinations []StartupDestination `json:"destinations,omitempty"`
	Recording    *StartupRecording    `json:"recording,omitempty"`
}

// StartupDestination is a DestinationConfig whose passphrase may be read from
// a file, so the file holding the rest of the configuration need not be
// secret.
type StartupDestination struct {
	DestinationConfig
	PassphraseFile string `json:"passphraseFile,omitempty"`
}

// StartupRecording takes the fields POST /api/recording/start takes. Unlike
// the API it has no default directory: a recording declared for a deployment
// names the volume it writes to.
type StartupRecording struct {
	OutputDir       string `json:"outputDir"`
	RotateAfterMins int    `json:"rotateAfterMins,omitempty"`
	MaxFileSizeMB   int    `json:"maxFileSizeMB,omitempty"`
	FilenamePrefix  string `json:"filenamePrefix,omitempty"`
}

// LoadStartupConfig reads and validates the startup outputs file, resolving
// every passphraseFile into the passphrase it names.
func LoadStartupConfig(path string) (StartupConfig, error) {
	var cfg StartupConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read outputs config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse outputs config %s: %w", path, err)
	}
	if dec.More() {
		return cfg, fmt.Errorf("parse outputs config %s: more than one JSON value", path)
	}
	for i := range cfg.Destinations {
		d := &cfg.Destinations[i]
		if d.PassphraseFile == "" {
			continue
		}
		if d.Passphrase != "" {
			return cfg, fmt.Errorf("outputs config: destination %d sets both passphrase and passphraseFile", i)
		}
		if !filepath.IsAbs(d.PassphraseFile) {
			return cfg, fmt.Errorf("outputs config: destination %d: passphraseFile must be an absolute path", i)
		}
		p, err := os.ReadFile(d.PassphraseFile)
		if err != nil {
			return cfg, fmt.Errorf("outputs config: destination %d: %w", i, err)
		}
		d.Passphrase = strings.TrimRight(string(p), "\r\n")
	}
	if cfg.Recording != nil {
		if _, err := cfg.Recording.RecorderConfig(); err != nil {
			return cfg, fmt.Errorf("outputs config: recording: %w", err)
		}
	}
	return cfg, nil
}

// RecorderConfig converts the request into the recorder's configuration.
func (r StartupRecording) RecorderConfig() (RecorderConfig, error) {
	if r.OutputDir == "" {
		return RecorderConfig{}, errors.New("outputDir is required")
	}
	if strings.Contains(r.OutputDir, "..") {
		return RecorderConfig{}, errors.New("outputDir must not contain path traversal (..)")
	}
	dir := filepath.Clean(r.OutputDir)
	if !filepath.IsAbs(dir) {
		return RecorderConfig{}, errors.New("outputDir must be an absolute path")
	}
	cfg := RecorderConfig{
		Dir:            dir,
		RotateAfter:    time.Hour,
		FilenamePrefix: r.FilenamePrefix,
	}
	if r.RotateAfterMins > 0 {
		cfg.RotateAfter = time.Duration(r.RotateAfterMins) * time.Minute
	}
	if r.MaxFileSizeMB > 0 {
		cfg.MaxFileSize = int64(r.MaxFileSizeMB) * 1024 * 1024
	}
	return cfg, nil
}

// ApplyStartup adds and starts every destination in cfg and starts the
// recording it names. It stops at the first one the manager refuses.
func (m *Manager) ApplyStartup(cfg StartupConfig) error {
	for i, d := range cfg.Destinations {
		id, err := m.AddDestination(d.DestinationConfig)
		if err != nil {
			return fmt.Errorf("destination %d (%s): %w", i, d.Type, err)
		}
		if err := m.StartDestination(id); err != nil {
			return fmt.Errorf("destination %d (%s): start: %w", i, d.Type, err)
		}
	}
	if cfg.Recording != nil {
		rc, err := cfg.Recording.RecorderConfig()
		if err != nil {
			return fmt.Errorf("recording: %w", err)
		}
		if err := m.StartRecording(rc); err != nil {
			return fmt.Errorf("recording: %w", err)
		}
	}
	return nil
}
