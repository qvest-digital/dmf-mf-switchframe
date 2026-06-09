package st2110

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// VideoReceiver abstracts an ST 2110-20 video RX session.
// Implementations provide decoded YUV420p frames from the MTL pipeline.
type VideoReceiver interface {
	// GetFrame blocks until a frame is available or ctx is cancelled.
	// If pts is non-zero, it is a PTP-derived timestamp (90 kHz);
	// zero means the caller should use wall-clock fallback.
	GetFrame(ctx context.Context) (yuv []byte, w, h int, pts int64, err error)
	Close() error
}

// AudioReceiver abstracts an ST 2110-30 audio RX session.
// Implementations provide interleaved float32 PCM from the MTL pipeline.
type AudioReceiver interface {
	// GetFrame blocks until audio samples are available or ctx is cancelled.
	GetFrame(ctx context.Context) (pcm []float32, samplesPerCh int, err error)
	Close() error
}

// AncillaryReceiver abstracts an ST 2110-40 ancillary data RX session.
// Implementations provide raw ancillary data packets from the MTL pipeline.
type AncillaryReceiver interface {
	// GetFrame blocks until ancillary data is available or ctx is cancelled.
	GetFrame(ctx context.Context) (data []byte, err error)
	Close() error
}

// SourceVideoSink is called with raw YUV420p frames for the switcher pipeline.
// key is the source identifier, yuv is YUV420p planar data.
type SourceVideoSink func(key string, yuv []byte, width, height int, pts int64)

// SourceAudioSink is called with interleaved float32 PCM for the mixer pipeline.
// channels is the source's configured channel count.
type SourceAudioSink func(key string, pcm []float32, pts int64, channels int)

// SourceConfig configures an ST 2110 source.
type SourceConfig struct {
	// Key is the unique source identifier.
	Key string

	// Channels is the audio channel count. Default: 2.
	Channels int

	// OnRawVideo delivers raw YUV420p to the switcher pipeline.
	OnRawVideo SourceVideoSink

	// OnRawAudio delivers interleaved float32 PCM to the mixer.
	OnRawAudio SourceAudioSink

	// OnDataGrain is called with raw ancillary data payloads.
	// key is the source identifier, data is the raw payload, pts is monotonic.
	OnDataGrain func(key string, data []byte, pts int64)

	// Snapshot, if set, receives per-session stats for debug reporting.
	Snapshot *ST2110Snapshot

	// Logger for diagnostic output.
	Logger *slog.Logger
}

// Source reads from ST 2110 receivers and fans out to switcher, mixer,
// and ancillary data consumers. It mirrors the mxl.Source pattern but
// operates on abstract receiver interfaces rather than MXL flow handles.
type Source struct {
	config SourceConfig
	log    *slog.Logger

	videoRx VideoReceiver
	audioRx AudioReceiver
	ancRx   AncillaryReceiver

	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once

	// Shared wall-clock epoch for AV sync. The first frame (video or audio)
	// to arrive sets the epoch; all subsequent wall-clock PTS values are
	// computed as offset from this shared reference.
	startTime time.Time
	startOnce sync.Once

	// Frame counters for debug snapshot. Each is written by its own fan-out
	// goroutine and read concurrently by Stats() (e.g. a debug/metrics
	// snapshot consumer), so they must be atomic to stay race-free.
	videoFrames atomic.Uint64
	audioFrames atomic.Uint64
	ancFrames   atomic.Uint64
}

// NewSource creates an ST 2110 source with the given configuration.
func NewSource(cfg SourceConfig) *Source {
	if cfg.Channels == 0 {
		cfg.Channels = 2
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Source{
		config: cfg,
		log:    log,
	}
}

// Start begins reading from ST 2110 receivers and distributing media.
// Non-nil receivers get their own fan-out goroutine. Nil receivers are skipped.
func (s *Source) Start(ctx context.Context, videoRx VideoReceiver, audioRx AudioReceiver, ancRx AncillaryReceiver) {
	ctx, s.cancel = context.WithCancel(ctx)

	if videoRx != nil {
		s.videoRx = videoRx
		s.wg.Add(1)
		go s.videoFanOut(ctx)
	}

	if audioRx != nil {
		s.audioRx = audioRx
		s.wg.Add(1)
		go s.audioFanOut(ctx)
	}

	if ancRx != nil {
		s.ancRx = ancRx
		s.wg.Add(1)
		go s.dataFanOut(ctx)
	}
}

// Stop halts the source and waits for goroutines to finish.
// Safe to call multiple times (idempotent via sync.Once).
// Closes receivers to release underlying resources.
func (s *Source) Stop() {
	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.wg.Wait()

		// Close receivers to release underlying resources (e.g. C handles).
		if s.videoRx != nil {
			_ = s.videoRx.Close()
		}
		if s.audioRx != nil {
			_ = s.audioRx.Close()
		}
		if s.ancRx != nil {
			_ = s.ancRx.Close()
		}
	})
}

// Key returns the source identifier from the config.
func (s *Source) Key() string {
	return s.config.Key
}

func (s *Source) videoFanOut(ctx context.Context) {
	defer s.wg.Done()

	for {
		yuv, w, h, pts, err := s.videoRx.GetFrame(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return // Normal shutdown.
			}
			s.log.Error("st2110 source: video GetFrame failed",
				"key", s.config.Key, "error", err)
			return
		}

		// PTS strategy: if the receiver provides a non-zero PTP timestamp,
		// use it directly. Otherwise fall back to wall-clock offset from
		// the shared epoch (same pattern as mxl/source.go).
		if pts == 0 {
			now := time.Now()
			s.startOnce.Do(func() { s.startTime = now })
			pts = int64(now.Sub(s.startTime).Seconds() * 90000)
		}

		s.videoFrames.Add(1)
		if s.config.OnRawVideo != nil {
			s.config.OnRawVideo(s.config.Key, yuv, w, h, pts)
		}
	}
}

func (s *Source) audioFanOut(ctx context.Context) {
	defer s.wg.Done()

	for {
		pcm, _, err := s.audioRx.GetFrame(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Error("st2110 source: audio GetFrame failed",
				"key", s.config.Key, "error", err)
			return
		}

		// Wall-clock PTS (shared epoch with video for AV sync).
		now := time.Now()
		s.startOnce.Do(func() { s.startTime = now })
		pts := int64(now.Sub(s.startTime).Seconds() * 90000)

		s.audioFrames.Add(1)
		if s.config.OnRawAudio != nil {
			s.config.OnRawAudio(s.config.Key, pcm, pts, s.config.Channels)
		}
	}
}

func (s *Source) dataFanOut(ctx context.Context) {
	defer s.wg.Done()

	for {
		data, err := s.ancRx.GetFrame(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Error("st2110 source: ancillary GetFrame failed",
				"key", s.config.Key, "error", err)
			return
		}

		// Wall-clock PTS for ancillary data.
		now := time.Now()
		s.startOnce.Do(func() { s.startTime = now })
		pts := int64(now.Sub(s.startTime).Seconds() * 90000)

		s.ancFrames.Add(1)
		if s.config.OnDataGrain != nil {
			s.config.OnDataGrain(s.config.Key, data, pts)
		}
	}
}

// Stats returns the current frame counters for debug reporting.
// Safe to call concurrently with the fan-out goroutines.
func (s *Source) Stats() SessionStats {
	return SessionStats{
		FramesReceived: s.videoFrames.Load() + s.audioFrames.Load() + s.ancFrames.Load(),
	}
}
