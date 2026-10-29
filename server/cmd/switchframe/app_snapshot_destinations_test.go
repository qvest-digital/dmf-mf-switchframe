package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/output"
)

// An engine that comes back without its state takes the peer's destinations
// over from the peer's snapshot under the same IDs: one the peer sends stays
// on standby here, with the config it needs to take over later.
func TestApplyOutputDestinations_AddsPeerDestinationsOnStandby(t *testing.T) {
	m := output.NewManager()
	t.Cleanup(func() { _ = m.Close() })
	m.SetEngineLabel("a")
	a := &App{outputMgr: m}

	armed := true
	require.NoError(t, a.ApplyOutputDestinations([]internal.DestinationSnapshot{{
		ID: "0a1b2c3d",
		Config: internal.DestinationSnapshotConfig{
			Type: "srt-caller", Address: "192.0.2.1", Port: 9000, Engine: "b",
			Encryption: "aes-128", Passphrase: "0123456789abcdef", SCTE35Enabled: true,
			AllowUntrustedCert: true,
		},
		Active: true,
		Armed:  &armed,
	}}))

	st, err := m.GetDestination("0a1b2c3d")
	require.NoError(t, err)
	require.Equal(t, "standby", st.State)
	require.Equal(t, "b", st.Config.Engine)
	require.Equal(t, "aes-128", st.Config.Encryption)
	require.Equal(t, "0123456789abcdef", st.Config.Passphrase)
	require.True(t, st.Config.SCTE35Enabled)
	require.True(t, st.Config.AllowUntrustedCert)
	require.Empty(t, m.SendingDestinationIDs())

	// A later snapshot that stopped it disarms it here too.
	armed = false
	require.NoError(t, a.ApplyOutputDestinations([]internal.DestinationSnapshot{{
		ID: "0a1b2c3d", Config: internal.DestinationSnapshotConfig{Type: "srt-caller", Engine: "b"}, Armed: &armed,
	}}))
	st, err = m.GetDestination("0a1b2c3d")
	require.NoError(t, err)
	require.Equal(t, "stopped", st.State)
}
