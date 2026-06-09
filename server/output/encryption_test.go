package output

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSRTKeyLength verifies the DestinationConfig.Encryption → srtgo AES key
// length mapping.
func TestSRTKeyLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		encryption string
		want       int
		wantErr    bool
	}{
		{"", 0, false},
		{"aes-128", 16, false},
		{"aes-192", 24, false},
		{"aes-256", 32, false},
		{"aes-512", 0, true},
		{"rot13", 0, true},
	}
	for _, tt := range tests {
		got, err := srtKeyLength(tt.encryption)
		if tt.wantErr {
			require.Error(t, err, "encryption %q", tt.encryption)
			continue
		}
		require.NoError(t, err, "encryption %q", tt.encryption)
		require.Equal(t, tt.want, got, "encryption %q", tt.encryption)
	}
}

// TestAddDestination_EncryptionValidation verifies that invalid encryption
// configs are rejected at AddDestination time instead of being silently
// ignored later.
func TestAddDestination_EncryptionValidation(t *testing.T) {
	t.Parallel()

	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	// Unknown encryption algorithm.
	_, err := mgr.AddDestination(DestinationConfig{
		Type: "srt-caller", Address: "127.0.0.1", Port: 9000,
		Encryption: "rot13", Passphrase: "super-secret-pass",
	})
	require.Error(t, err)

	// Encryption without a passphrase.
	_, err = mgr.AddDestination(DestinationConfig{
		Type: "srt-caller", Address: "127.0.0.1", Port: 9000,
		Encryption: "aes-256",
	})
	require.Error(t, err)

	// Passphrase too short (SRT requires 10-79 chars).
	_, err = mgr.AddDestination(DestinationConfig{
		Type: "srt-caller", Address: "127.0.0.1", Port: 9000,
		Encryption: "aes-256", Passphrase: "short",
	})
	require.Error(t, err)

	// Encryption on an RTMP destination is not supported.
	_, err = mgr.AddDestination(DestinationConfig{
		Type: "rtmp", URL: "rtmp://example.com/live",
		Encryption: "aes-256", Passphrase: "super-secret-pass",
	})
	require.Error(t, err)

	// Valid encrypted SRT caller config is accepted.
	_, err = mgr.AddDestination(DestinationConfig{
		Type: "srt-caller", Address: "127.0.0.1", Port: 9000,
		Encryption: "aes-256", Passphrase: "super-secret-pass",
	})
	require.NoError(t, err)

	// Passphrase without explicit encryption defaults to AES-128 — accepted.
	_, err = mgr.AddDestination(DestinationConfig{
		Type: "srt-listener", Port: 9001,
		Passphrase: "super-secret-pass",
	})
	require.NoError(t, err)
}

// TestStartDestination_CallerEncryptionWiredThrough verifies that an
// encrypted srt-caller destination passes its passphrase and key length
// through to the SRT connect function (finding: encryption config was
// silently dropped and 'encrypted' destinations streamed in cleartext).
func TestStartDestination_CallerEncryptionWiredThrough(t *testing.T) {
	t.Parallel()

	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	gotCfg := make(chan SRTCallerConfig, 1)
	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			gotCfg <- config
			return &mockSRTConn{}, nil
		},
		nil,
	)

	id, err := mgr.AddDestination(DestinationConfig{
		Type:       "srt-caller",
		Address:    "127.0.0.1",
		Port:       9000,
		Encryption: "aes-256",
		Passphrase: "super-secret-pass",
	})
	require.NoError(t, err)
	require.NoError(t, mgr.StartDestination(id))

	select {
	case cfg := <-gotCfg:
		require.Equal(t, "super-secret-pass", cfg.Passphrase,
			"passphrase must be wired through to the SRT dial config")
		require.Equal(t, 32, cfg.KeyLength,
			"aes-256 must map to a 32-byte key length")
	case <-time.After(2 * time.Second):
		t.Fatal("connectFn was never invoked")
	}
}

// TestStartDestination_ListenerEncryptionWiredThrough verifies that an
// encrypted srt-listener destination passes its passphrase and key length
// through to the SRT accept loop config.
func TestStartDestination_ListenerEncryptionWiredThrough(t *testing.T) {
	t.Parallel()

	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	gotCfg := make(chan SRTListenerConfig, 1)
	mgr.SetSRTWiring(
		nil,
		func(ctx context.Context, config SRTListenerConfig, listener *SRTListener) error {
			gotCfg <- config
			<-ctx.Done()
			return nil
		},
	)

	id, err := mgr.AddDestination(DestinationConfig{
		Type:       "srt-listener",
		Port:       9001,
		Encryption: "aes-128",
		Passphrase: "another-secret-pass",
	})
	require.NoError(t, err)
	require.NoError(t, mgr.StartDestination(id))

	select {
	case cfg := <-gotCfg:
		require.Equal(t, "another-secret-pass", cfg.Passphrase,
			"passphrase must be wired through to the SRT listen config")
		require.Equal(t, 16, cfg.KeyLength,
			"aes-128 must map to a 16-byte key length")
	case <-time.After(2 * time.Second):
		t.Fatal("acceptFn was never invoked")
	}
}
