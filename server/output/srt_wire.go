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

// SRTConnect creates a real SRT caller connection using zsiec/srtgo.
// It dials the remote SRT endpoint with the given configuration.
//
// The ctx parameter is accepted for API compatibility but is not currently
// passed to srt.Dial, which does not support context-based cancellation.
// Connection timeout is controlled by srt.Config.ConnTimeout (default 5s).
func SRTConnect(ctx context.Context, config SRTCallerConfig) (srtConn, error) {
	latency := config.Latency
	if latency == 0 {
		latency = defaultSRTLatency
	}

	cfg := srt.Config{
		Latency:     time.Duration(latency) * time.Millisecond,
		StreamID:    config.StreamID,
		ConnTimeout: 5 * time.Second,
	}

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
	latency := config.Latency
	if latency == 0 {
		latency = defaultSRTLatency
	}

	cfg := srt.Config{
		Latency: time.Duration(latency) * time.Millisecond,
	}

	addr := fmt.Sprintf(":%d", config.Port)
	ln, err := srt.Listen(addr, cfg)
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
			slog.Warn("SRT accept error", "error", err)
			continue
		}

		id := fmt.Sprintf("srt-%d", srtConnCounter.Add(1))
		if err := listener.AddConnection(id, &chunkedConn{inner: &srtgoConn{conn: conn}}); err != nil {
			slog.Warn("SRT reject connection (max reached)", "error", err)
			_ = conn.Close()
		}
	}
}

// Compile-time check that srtgoConn implements srtConn.
var _ srtConn = (*srtgoConn)(nil)
