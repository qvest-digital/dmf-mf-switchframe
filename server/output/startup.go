package output

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
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
//	    {"type": "rtmp", "urlFile": "/run/secrets/rtmp/url"}
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

// StartupDestination is a DestinationConfig whose SRT passphrase and RTMP URL
// may be read from files, so the file holding the rest of the configuration
// need not be secret: an RTMP URL usually carries the stream key.
type StartupDestination struct {
	DestinationConfig
	PassphraseFile string `json:"passphraseFile,omitempty"`
	URLFile        string `json:"urlFile,omitempty"`
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
		if err := readInto(&d.Passphrase, d.PassphraseFile, "passphrase"); err != nil {
			return cfg, fmt.Errorf("outputs config: destination %d: %w", i, err)
		}
		if err := readInto(&d.URL, d.URLFile, "url"); err != nil {
			return cfg, fmt.Errorf("outputs config: destination %d: %w", i, err)
		}
	}
	if cfg.Recording != nil {
		if _, err := cfg.Recording.RecorderConfig(); err != nil {
			return cfg, fmt.Errorf("outputs config: recording: %w", err)
		}
	}
	return cfg, nil
}

// startupDestinationID derives a startup destination's ID from where it
// sends rather than from its position in the file: during a rolling update
// the two engines of a pair may read files that list the destinations in
// another order, and one ID has to name one target on both.
func startupDestinationID(d DestinationConfig) string {
	target := d.URL
	if d.Type != "rtmp" {
		target = net.JoinHostPort(d.Address, strconv.Itoa(d.Port))
	}
	sum := sha256.Sum256([]byte("outputs-config/destination\x00" + d.Type + "\x00" + target + "\x00" + d.StreamID))
	return hex.EncodeToString(sum[:4])
}

// readInto sets *field from the file at path, without the trailing newline a
// mounted Secret often carries. An empty path leaves the field alone.
func readInto(field *string, path, name string) error {
	if path == "" {
		return nil
	}
	if *field != "" {
		return fmt.Errorf("sets both %s and %sFile", name, name)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%sFile must be an absolute path", name)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	*field = strings.TrimRight(string(b), "\r\n")
	return nil
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
//
// Each destination gets an ID derived from its type and target, so both
// engines of a pair store it under one ID. With peer
// failover enabled, a destination one engine sends is armed and left to
// ReconcileDestinations, which starts it once the peer is known not to send
// it already.
func (m *Manager) ApplyStartup(cfg StartupConfig) error {
	m.mu.Lock()
	deferStart := m.peerFailover && m.engineLabel != ""
	m.mu.Unlock()
	for i, d := range cfg.Destinations {
		id, err := m.AddDestinationWithID(startupDestinationID(d.DestinationConfig), d.DestinationConfig)
		if errors.Is(err, ErrDestinationExists) {
			return fmt.Errorf("destination %d (%s): same type, target and stream ID as another one", i, d.Type)
		}
		if err != nil {
			return fmt.Errorf("destination %d (%s): %w", i, d.Type, err)
		}
		if deferStart && d.Type != "srt-listener" {
			err = m.armDestination(id)
		} else {
			err = m.StartDestination(id)
		}
		if err != nil {
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
