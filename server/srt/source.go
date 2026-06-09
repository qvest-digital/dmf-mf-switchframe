package srt

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	srtgo "github.com/zsiec/srtgo"
)

// SRTStatter is implemented by connections that can report SRT-level statistics.
// srtgo.Conn satisfies this interface. Tests can use a mock.
type SRTStatter interface {
	Stats(clear bool) srtgo.ConnStats
}

// streamDecoder abstracts the StreamDecoder for testability.
type streamDecoder interface {
	Run()
	Stop()
}

// decoderFactoryFunc creates a streamDecoder from a config.
// The default implementation creates a real StreamDecoder (cgo/FFmpeg).
type decoderFactoryFunc func(cfg StreamDecoderConfig) (streamDecoder, error)

// Source orchestrates an SRT input source: connects the StreamDecoder to the
// switcher pipeline. Same fan-out pattern as mxl.Source.
type Source struct {
	config    SourceConfig
	conn      io.ReadCloser // srtgo.Conn typed as io.ReadCloser for testability
	stats     *ConnStats
	log       *slog.Logger
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	stopped   sync.Once
	connClose sync.Once

	// decoderFactory creates the stream decoder. Injected for testing;
	// defaults to newRealDecoder which creates a real StreamDecoder.
	decoderFactory decoderFactoryFunc

	// Callbacks — set by the app layer before Start().
	OnRawVideo    func(key string, yuv []byte, width, height int, pts int64)
	OnRawVideoGPU func(key string, yDevPtr, uvDevPtr uintptr, pitch, width, height int, pts int64, bitDepth int)
	OnRawAudio    func(key string, pcm []float32, pts int64, sampleRate, channels int)
	OnCaptions    func(key string, data []byte, pts int64)
	OnSCTE35      func(key string, data []byte, pts int64)
	OnStopped     func(key string)
}

// NewSource creates an SRT source orchestrator.
func NewSource(config SourceConfig, conn io.ReadCloser, stats *ConnStats, log *slog.Logger) *Source {
	if log == nil {
		log = slog.Default()
	}
	return &Source{
		config: config,
		conn:   conn,
		stats:  stats,
		log:    log,
	}
}

// Start begins the decode loop and stats polling. Non-blocking.
func (s *Source) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)

	factory := s.decoderFactory
	if factory == nil {
		factory = newRealDecoder
	}

	dec, err := factory(StreamDecoderConfig{
		Reader: s.conn,
		OnVideo: func(yuv []byte, width, height int, pts int64) {
			s.handleVideoFrame(yuv, width, height, pts)
		},
		OnVideoGPU: func(yDevPtr, uvDevPtr uintptr, pitch, w, h int, pts int64, bitDepth int) {
			if s.OnRawVideoGPU != nil {
				s.OnRawVideoGPU(s.config.Key, yDevPtr, uvDevPtr, pitch, w, h, pts, bitDepth)
			}
		},
		OnAudio: func(pcm []float32, pts int64, sampleRate, channels int) {
			s.handleAudioFrame(pcm, pts, sampleRate, channels)
		},
		OnCaptions: func(data []byte, pts int64) {
			if s.OnCaptions != nil {
				s.OnCaptions(s.config.Key, data, pts)
			}
		},
		OnSCTE35: func(data []byte, pts int64) {
			if s.OnSCTE35 != nil {
				s.OnSCTE35(s.config.Key, data, pts)
			}
		},
		HWDeviceCtx: s.config.HWDeviceCtx,
	})
	if err != nil {
		return err
	}

	// Decode goroutine: runs the decoder until EOF/error/stop, then signals OnStopped.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		dec.Run()
		s.log.Info("srt source: decode loop ended", "key", s.config.Key)
		// Release resources on every termination path, including a natural
		// decode EOF/disconnect that never goes through Stop(). Cancelling the
		// context unblocks the context-watcher and stats-poller goroutines, and
		// closing the conn releases the SRT socket (dec.Run() already ran the
		// decoder's close(), so its Stop() will not close the reader).
		s.cancel()
		s.closeConn()
		if s.OnStopped != nil {
			s.OnStopped(s.config.Key)
		}
	}()

	// Context watcher: stops the decoder when the context is cancelled.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		<-ctx.Done()
		dec.Stop()
	}()

	// Stats poller: polls srtgo connection stats every second.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.pollStats(ctx)
	}()

	return nil
}

// Stop cancels the decode loop and waits for all goroutines to finish.
// Safe to call multiple times.
func (s *Source) Stop() {
	s.stopped.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.wg.Wait()
		// Ensure the SRT socket is released even if the decode goroutine did not
		// (e.g. Start() was never called, or the decoder's Stop() path did not
		// close the reader). closeConn is idempotent via sync.Once.
		s.closeConn()
	})
}

// closeConn releases the underlying SRT connection exactly once. It is invoked
// from both the natural-EOF teardown in the decode goroutine and from Stop(),
// so the socket is freed on every termination path without double-closing.
func (s *Source) closeConn() {
	s.connClose.Do(func() {
		if s.conn != nil {
			_ = s.conn.Close()
		}
	})
}

// Config returns the source configuration.
func (s *Source) Config() SourceConfig {
	return s.config
}

// handleVideoFrame delegates decoded video to the OnRawVideo callback.
func (s *Source) handleVideoFrame(yuv []byte, width, height int, pts int64) {
	if s.OnRawVideo != nil {
		s.OnRawVideo(s.config.Key, yuv, width, height, pts)
	}
}

// handleAudioFrame delegates decoded audio to the OnRawAudio callback.
func (s *Source) handleAudioFrame(pcm []float32, pts int64, sampleRate, channels int) {
	if s.OnRawAudio != nil {
		s.OnRawAudio(s.config.Key, pcm, pts, sampleRate, channels)
	}
}

// pollStats polls the SRT connection for stats every second and updates ConnStats.
func (s *Source) pollStats(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.updateStats(ctx)
		}
	}
}

// updateStats fetches SRT stats from the connection and updates ConnStats.
// It no-ops once ctx is cancelled (source stopped or decode loop ended) so a
// poller tick that races the cancellation cannot write stale metrics from a
// dead conn into the shared per-key ConnStats.
func (s *Source) updateStats(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	statter, ok := s.conn.(SRTStatter)
	if !ok {
		return
	}

	// Interval mode: MbpsRecvRate and loss rate reflect the recent window.
	// The packet counters are also interval deltas; ConnStats.Update
	// accumulates them into the lifetime totals exposed by snapshots.
	cs := statter.Stats(true)

	s.stats.Update(StatsUpdate{
		RTTMs:           float64(cs.RTT) / float64(time.Millisecond),
		RTTVarMs:        float64(cs.RTTVar) / float64(time.Millisecond),
		RecvRateMbps:    cs.MbpsRecvRate,
		LossRatePct:     cs.RecvLossRate,
		PacketsReceived: int64(cs.RecvPackets),
		PacketsLost:     int64(cs.RecvLoss),
		PacketsDropped:  int64(cs.RecvDropped),
		PacketsRetrans:  int64(cs.Retransmits),
		PacketsBelated:  int64(cs.RecvBelated),
		RecvBufMs:       float64(cs.MsRcvBuf) / float64(time.Millisecond),
		RecvBufPackets:  cs.RecvBufSize,
		FlightSize:      cs.FlightSize,
	})
}

// newRealDecoder wraps NewStreamDecoder to satisfy the decoderFactoryFunc signature.
// This is the default factory used in production (requires cgo).
func newRealDecoder(cfg StreamDecoderConfig) (streamDecoder, error) {
	return NewStreamDecoder(cfg)
}
