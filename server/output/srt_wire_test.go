package output

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time interface check.
var _ srtConn = (*srtgoConn)(nil)

func TestSRTConnect_InvalidAddress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := SRTConnect(ctx, SRTCallerConfig{
		Address: "192.0.2.1", // TEST-NET, unreachable
		Port:    0,           // invalid port
		Latency: 120,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "srt dial")
}

func TestManagerSetSRTWiring(t *testing.T) {
	mgr := NewOutputManager(nil)

	mgr.SetSRTWiring(
		func(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
			return nil, nil
		},
		func(ctx context.Context, config SRTListenerConfig, listener *SRTListener) error {
			return nil
		},
	)

	assert.NotNil(t, mgr.srtConnectFn)
	assert.NotNil(t, mgr.srtAcceptFn)
}
