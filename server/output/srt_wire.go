package output

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	srt "github.com/zsiec/srtgo"
)

// srtConnCounter provides globally unique connection IDs across SRT listener restarts.
var srtConnCounter atomic.Int64

// srtgoConn wraps a zsiec/srtgo Conn to implement the srtConn interface.
type srtgoConn struct {
	conn *srt.Conn
}

func (c *srtgoConn) Write(data []byte) (int, error) {
	return c.conn.Write(data)
}

func (c *srtgoConn) Close() {
	_ = c.conn.Close()
}

// SRTStats returns SRT connection statistics from the underlying srtgo connection.
func (c *srtgoConn) SRTStats(clear bool) SRTConnStats {
	cs := c.conn.Stats(clear)
	return SRTConnStats{
		RTT:          cs.RTT,
		RTTVar:       cs.RTTVar,
		MbpsSendRate: cs.MbpsSendRate,
		SendLossRate: cs.SendLossRate,
		SendPackets:  cs.SentPackets,
		SendLoss:     cs.LostPackets,
		SendDropped:  cs.SentDropped,
		Retransmits:  cs.Retransmits,
		MsSndBuf:     cs.MsSndBuf,
		SendBufSize:  cs.SendBufSize,
		FlightSize:   cs.FlightSize,
	}
}

// srtListen is srt.Listen, replaceable in tests.
var srtListen = srt.Listen

// srtOutputConfig is the srtgo configuration of a sending connection, in
// caller and listener mode alike.
func srtOutputConfig(latencyMs int, passphrase string, keyLength int, inputBW int64, overheadBW int) srt.Config {
	if latencyMs == 0 {
		latencyMs = defaultSRTLatency
	}
	cfg := srt.Config{
		Latency:    time.Duration(latencyMs) * time.Millisecond,
		Passphrase: passphrase,
		KeyLength:  keyLength,
		// Close returns at once instead of waiting for unacknowledged data;
		// connections are closed under the adapters' locks.
		Linger: -1,
		// Pace at the line rate. Left at 0, srtgo paces at 1.25 times the
		// input rate it sampled, which spreads every keyframe burst out
		// over the following frames.
		MaxBW: srt.DefaultMaxBW,
	}

	// When InputBW is set, tell SRT the expected input rate so it can
	// derive MaxBW = InputBW * (1 + OverheadBW/100) instead of relying
	// on auto-estimation which chases VBR spikes.
	if inputBW > 0 {
		cfg.InputBW = inputBW
		cfg.MaxBW = 0 // auto = InputBW * (1 + OverheadBW/100)
		if overheadBW > 0 {
			cfg.OverheadBW = overheadBW
		}
	}
	return cfg
}

// SRTConnect creates a real SRT caller connection using zsiec/srtgo.
// It dials the remote SRT endpoint with the given configuration.
//
// The ctx parameter is accepted for API compatibility but is not currently
// passed to srt.Dial, which does not support context-based cancellation.
// Connection timeout is controlled by srt.Config.ConnTimeout (default 5s).
func SRTConnect(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
	cfg := srtOutputConfig(config.Latency, config.Passphrase, config.KeyLength, config.InputBW, config.OverheadBW)
	cfg.StreamID = config.StreamID
	cfg.ConnTimeout = 5 * time.Second

	addr := fmt.Sprintf("%s:%d", config.Address, config.Port)
	conn, err := srt.Dial(addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("srt dial %s: %w", addr, err)
	}

	return &chunkedConn{inner: &srtgoConn{conn: conn}}, nil
}

// SRTAcceptLoop runs the SRT listener accept loop. It binds on the configured
// port and accepts incoming connections, adding each to the SRTListener.
// The loop runs until ctx is cancelled.
func SRTAcceptLoop(ctx context.Context, config SRTListenerConfig, listener *SRTListener) error {
	cfg := srtOutputConfig(config.Latency, config.Passphrase, config.KeyLength, config.InputBW, config.OverheadBW)

	addr := fmt.Sprintf(":%d", config.Port)
	ln, err := srtListen(addr, cfg)
	if err != nil {
		return fmt.Errorf("srt listen %s: %w", addr, err)
	}

	// Close the listener when context is cancelled.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Check if context was cancelled (normal shutdown).
			if ctx.Err() != nil {
				return nil
			}
			// Accept fails only once the listener is closed, and from
			// then on at once on every call.
			return fmt.Errorf("srt accept on %s: %w", addr, err)
		}

		id := fmt.Sprintf("srt-%d", srtConnCounter.Add(1))
		if err := listener.AddConnection(id, &chunkedConn{inner: &srtgoConn{conn: conn}}); err != nil {
			slog.Warn("SRT reject connection (max reached)", "error", err)
			_ = conn.Close()
		}
	}
}

// Compile-time checks.
var _ srtConn = (*srtgoConn)(nil)
var _ SRTStatter = (*srtgoConn)(nil)
var _ SRTStatter = (*chunkedConn)(nil)
