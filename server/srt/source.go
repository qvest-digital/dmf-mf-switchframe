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
	config  SourceConfig
	conn    io.ReadCloser // srtgo.Conn typed as io.ReadCloser for testability
	stats   *ConnStats
	log     *slog.Logger
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	stopped sync.Once

	// decoderFactory creates the stream decoder. Injected for testing;
	// defaults to newRealDecoder which creates a real StreamDecoder.
	decoderFactory decoderFactoryFunc

	// Callbacks — set by the app layer before Start().
	OnRawVideo func(key string, yuv []byte, width, height int, pts int64)
	OnRawAudio func(key string, pcm []float32, pts int64, sampleRate, channels int)
	OnCaptions func(key string, data []byte, pts int64)
	OnSCTE35   func(key string, data []byte, pts int64)
	OnStopped  func(key string)
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
			s.updateStats()
		}
	}
}

// updateStats fetches SRT stats from the connection and updates ConnStats.
func (s *Source) updateStats() {
	statter, ok := s.conn.(SRTStatter)
	if !ok {
		return
	}

	cs := statter.Stats(false) // cumulative stats

	rttMs := float64(cs.RTT) / float64(time.Millisecond)
	rttVarMs := float64(cs.RTTVar) / float64(time.Millisecond)
	recvBufMs := float64(cs.MsRcvBuf) / float64(time.Millisecond)

	// Compute receive bitrate from MbpsRecvRate if available,
	// otherwise approximate from bytes/duration.
	recvRateMbps := cs.MbpsRecvRate

	// Loss rate as percentage
	lossRatePct := cs.RecvLossRate

	s.stats.Update(
		rttMs,
		rttVarMs,
		recvRateMbps,
		lossRatePct,
		int64(cs.RecvPackets),
		int64(cs.RecvLoss),
		int64(cs.RecvDropped),
		int64(cs.Retransmits),
		int64(cs.RecvBelated),
		recvBufMs,
		cs.RecvBufSize,
		cs.FlightSize,
	)
}

// newRealDecoder wraps NewStreamDecoder to satisfy the decoderFactoryFunc signature.
// This is the default factory used in production (requires cgo).
func newRealDecoder(cfg StreamDecoderConfig) (streamDecoder, error) {
	return NewStreamDecoder(cfg)
}
