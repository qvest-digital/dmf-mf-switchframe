package mxl

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// SourceVideoSink is called with raw YUV420p frames for the switcher pipeline.
// key is the source identifier, yuv is YUV420p planar data.
type SourceVideoSink func(key string, yuv []byte, width, height int, pts int64)

// SourceAudioSink is called with interleaved float32 PCM for the mixer pipeline.
type SourceAudioSink func(key string, pcm []float32, pts int64)

// MediaBroadcaster sends encoded media to viewers (browser relay).
type MediaBroadcaster interface {
	BroadcastVideo(frame *media.VideoFrame)
	BroadcastAudio(frame *media.AudioFrame)
}

// SourceConfig configures an MXL source.
type SourceConfig struct {
	// FlowName is the human-readable source name.
	FlowName string

	// VideoFlowID is the MXL flow UUID for video.
	VideoFlowID string

	// AudioFlowID is the MXL flow UUID for audio.
	AudioFlowID string

	// Width and Height of the video source.
	Width  int
	Height int

	// SampleRate and Channels for audio. Defaults: 48000, 2.
	SampleRate int
	Channels   int

	// FPSNum and FPSDen express the frame rate as a rational number (e.g. 30000/1001
	// for 29.97fps). Used for PTS conversion and encoder creation. Defaults: 30000/1001.
	FPSNum int
	FPSDen int

	// Bitrate for H.264 encoding (relay path). Default: 6 Mbps.
	Bitrate int

	// OnRawVideo delivers raw YUV420p to the switcher pipeline.
	OnRawVideo SourceVideoSink

	// OnRawAudio delivers interleaved float32 PCM to the mixer.
	OnRawAudio SourceAudioSink

	// OnDataGrain is called with raw data grain payloads (metadata/ancillary).
	// key is the source identifier, data is the raw payload, pts is the monotonic counter.
	OnDataGrain func(key string, data []byte, pts int64)

	// Relay broadcasts H.264/AAC to browsers and replay.
	Relay MediaBroadcaster

	// EncoderFactory creates H.264 encoders for the browser relay path.
	EncoderFactory transition.EncoderFactory

	// AudioEncoderFactory creates AAC encoders for the browser relay path.
	// If nil, audio is not encoded for relay.
	AudioEncoderFactory func(sampleRate, channels int) (AudioEnc, error)

	// OnVideoInfo is called once after the first keyframe is encoded,
	// providing SPS/PPS so the caller can set VideoInfo on the relay
	// (required for browser decoder initialization).
	OnVideoInfo func(sps, pps []byte, width, height int)

	Logger *slog.Logger
}

// AudioEnc encodes PCM to AAC.
type AudioEnc interface {
	Encode(pcm []float32) ([]byte, error)
	Close() error
}

// v210Buffers holds pre-allocated buffers for V210↔YUV420p conversion,
// eliminating per-frame allocation on the hot path.
type v210Buffers struct {
	yuvOut   []byte // YUV420p output: w*h + 2*(w/2)*(h/2)
	cb422Tmp []byte // temporary 4:2:2 chroma (2 rows): (w/2)*2
	cr422Tmp []byte // temporary 4:2:2 chroma (2 rows): (w/2)*2
	width    int
	height   int
}

func (vb *v210Buffers) ensureSize(width, height int) {
	if vb.width == width && vb.height == height {
		return
	}
	ySize := width * height
	chromaW := width / 2
	cSize := chromaW * (height / 2)
	vb.yuvOut = make([]byte, ySize+2*cSize)
	vb.cb422Tmp = make([]byte, chromaW*2)
	vb.cr422Tmp = make([]byte, chromaW*2)
	vb.width = width
	vb.height = height
}

// Source reads from an MXL flow and fans out to switcher, mixer, and relay.
type Source struct {
	config SourceConfig
	log    *slog.Logger

	videoReader *Reader
	audioReader *Reader
	dataReader  *Reader

	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Shared wall-clock epoch for AV sync. The first grain (video or audio)
	// to arrive sets the epoch; all subsequent PTS values are computed as
	// wall-clock offset from this shared reference. This ensures that if
	// video starts 200ms after audio, the video PTS reflects that delay.
	startTime time.Time
	startOnce sync.Once

	// Running encoder for video relay path.
	videoEncoder  transition.VideoEncoder
	audioEncoder  AudioEnc
	groupID       atomic.Uint32
	videoInfoSent bool

	// Pre-allocated V210 conversion buffers (used from single videoFanOut goroutine).
	v210Bufs v210Buffers
}

// NewSource creates an MXL source.
func NewSource(config SourceConfig) *Source {
	if config.FPSNum == 0 {
		config.FPSNum = 30000
	}
	if config.FPSDen == 0 {
		config.FPSDen = 1001
	}
	if config.SampleRate == 0 {
		config.SampleRate = 48000
	}
	if config.Channels == 0 {
		config.Channels = 2
	}
	log := config.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Source{
		config: config,
		log:    log,
	}
}

// Start begins reading from MXL flows and distributing media.
// videoFlow, audioFlow, and dataFlow are the MXL flow readers (can be nil to skip).
func (s *Source) Start(ctx context.Context, videoFlow DiscreteReader, audioFlow ContinuousReader, dataFlow ...DiscreteReader) {
	ctx, s.cancel = context.WithCancel(ctx)

	if videoFlow != nil {
		s.videoReader = NewVideoReader(ReaderConfig{
			BufSize:   4,
			TimeoutMs: 100,
			Width:     s.config.Width,
			Height:    s.config.Height,
			Logger:    s.log,
		})
		s.videoReader.StartVideo(ctx, videoFlow)
		s.wg.Add(1)
		go s.videoFanOut(ctx)
	}

	if audioFlow != nil {
		s.audioReader = NewAudioReader(ReaderConfig{
			BufSize:        16,
			TimeoutMs:      100, // overridden by audioLoop's 5ms for actual reads
			SamplesPerRead: 1024,
			Logger:         s.log,
		})
		s.audioReader.StartAudio(ctx, audioFlow)
		s.wg.Add(1)
		go s.audioFanOut(ctx)
	}

	// Optional data flow (metadata/ancillary).
	if len(dataFlow) > 0 && dataFlow[0] != nil {
		s.dataReader = NewDataReader(ReaderConfig{
			BufSize:   8,
			TimeoutMs: 100,
			Logger:    s.log,
		})
		s.dataReader.StartData(ctx, dataFlow[0])
		s.wg.Add(1)
		go s.dataFanOut(ctx)
	}
}

// Stop halts the source and waits for goroutines to finish.
func (s *Source) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()

	if s.videoEncoder != nil {
		s.videoEncoder.Close()
	}
	if s.audioEncoder != nil {
		_ = s.audioEncoder.Close()
	}
}

func (s *Source) videoFanOut(ctx context.Context) {
	defer s.wg.Done()

	videoCh := s.videoReader.Video()
	if videoCh == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case grain, ok := <-videoCh:
			if !ok {
				return
			}
			s.processVideoGrain(grain)
		}
	}
}

func (s *Source) processVideoGrain(grain VideoGrain) {
	// 1. Convert V210 → YUV420p using pre-allocated buffers.
	s.v210Bufs.ensureSize(grain.Width, grain.Height)
	err := V210ToYUV420pInto(grain.V210, s.v210Bufs.yuvOut, s.v210Bufs.cb422Tmp, s.v210Bufs.cr422Tmp, grain.Width, grain.Height)
	if err != nil {
		s.log.Error("mxl source: V210→YUV420p conversion failed",
			"error", err, "width", grain.Width, "height", grain.Height)
		return
	}
	yuv := s.v210Bufs.yuvOut

	// Use wall-clock time for PTS to maintain AV sync across independently-
	// started video and audio flows. Both share the same epoch (set by
	// whichever flow produces the first grain).
	s.startOnce.Do(func() { s.startTime = grain.ReadTime })
	pts := int64(grain.ReadTime.Sub(s.startTime).Seconds() * 90000)

	// 2. Deliver raw YUV to switcher pipeline.
	if s.config.OnRawVideo != nil {
		s.config.OnRawVideo(s.config.FlowName, yuv, grain.Width, grain.Height, pts)
	}

	// 3. Encode YUV→H.264 and broadcast to relay for browsers + replay.
	if s.config.Relay != nil && s.config.EncoderFactory != nil {
		s.encodeAndBroadcastVideo(yuv, grain.Width, grain.Height, pts)
	}
}

// encodeAndBroadcastVideo encodes a YUV420p frame to H.264 and broadcasts it.
// Called only from the single videoFanOut goroutine, so lazy-init is safe.
func (s *Source) encodeAndBroadcastVideo(yuv []byte, width, height int, pts int64) {
	if s.videoEncoder == nil {
		bitrate := s.config.Bitrate
		if bitrate == 0 {
			bitrate = 6_000_000
		}
		enc, err := s.config.EncoderFactory(width, height, bitrate, s.config.FPSNum, s.config.FPSDen)
		if err != nil {
			s.log.Error("mxl source: failed to create video encoder", "error", err)
			return
		}
		s.videoEncoder = enc
	}

	encoded, isKeyframe, err := s.videoEncoder.Encode(yuv, pts, false)
	if err != nil {
		s.log.Error("mxl source: video encode failed", "error", err)
		return
	}
	if len(encoded) == 0 {
		return // Encoder warming up.
	}

	avc1 := codec.AnnexBToAVC1(encoded)

	if isKeyframe {
		s.groupID.Add(1)
	}

	frame := &media.VideoFrame{
		PTS:        pts,
		DTS:        pts,
		IsKeyframe: isKeyframe,
		WireData:   avc1,
		Codec:      "h264",
		GroupID:    s.groupID.Load(),
	}

	if isKeyframe {
		for _, nalu := range codec.ExtractNALUs(avc1) {
			if len(nalu) == 0 {
				continue
			}
			switch nalu[0] & 0x1F {
			case 7:
				frame.SPS = nalu
			case 8:
				frame.PPS = nalu
			}
		}

		// Notify caller on first keyframe so it can set VideoInfo on the relay.
		// Browsers need this to initialize their VideoDecoder.
		if !s.videoInfoSent && s.config.OnVideoInfo != nil && frame.SPS != nil && frame.PPS != nil {
			s.videoInfoSent = true
			s.config.OnVideoInfo(frame.SPS, frame.PPS, width, height)
		}
	}

	s.config.Relay.BroadcastVideo(frame)
}

func (s *Source) audioFanOut(ctx context.Context) {
	defer s.wg.Done()

	audioCh := s.audioReader.Audio()
	if audioCh == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case grain, ok := <-audioCh:
			if !ok {
				return
			}
			s.processAudioGrain(grain)
		}
	}
}

func (s *Source) dataFanOut(ctx context.Context) {
	defer s.wg.Done()

	dataCh := s.dataReader.Data()
	if dataCh == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case grain, ok := <-dataCh:
			if !ok {
				return
			}
			if s.config.OnDataGrain != nil {
				s.config.OnDataGrain(s.config.FlowName, grain.Data, grain.PTS)
			}
		}
	}
}

func (s *Source) processAudioGrain(grain AudioGrain) {
	// MXL audio is de-interleaved. Convert to interleaved for mixer.
	interleaved := interleaveChannels(grain.PCM)

	// Use wall-clock time for PTS (shared epoch with video for AV sync).
	s.startOnce.Do(func() { s.startTime = grain.ReadTime })
	pts := int64(grain.ReadTime.Sub(s.startTime).Seconds() * 90000)

	// 1. Deliver raw PCM to mixer.
	if s.config.OnRawAudio != nil {
		s.config.OnRawAudio(s.config.FlowName, interleaved, pts)
	}

	// 2. Encode PCM→AAC and broadcast to relay.
	if s.config.Relay != nil && s.config.AudioEncoderFactory != nil {
		s.encodeAndBroadcastAudio(interleaved, grain.SampleRate, grain.Channels, pts)
	}
}

// encodeAndBroadcastAudio encodes PCM to AAC and broadcasts it.
// Called only from the single audioFanOut goroutine, so lazy-init is safe.
func (s *Source) encodeAndBroadcastAudio(pcm []float32, sampleRate, channels int, pts int64) {
	if s.audioEncoder == nil {
		enc, err := s.config.AudioEncoderFactory(sampleRate, channels)
		if err != nil {
			s.log.Error("mxl source: failed to create audio encoder", "error", err)
			return
		}
		s.audioEncoder = enc
	}

	encoded, err := s.audioEncoder.Encode(pcm)
	if err != nil {
		s.log.Error("mxl source: audio encode failed", "error", err)
		return
	}
	if len(encoded) == 0 {
		return
	}

	frame := &media.AudioFrame{
		PTS:        pts,
		Data:       encoded,
		SampleRate: sampleRate,
		Channels:   channels,
	}
	s.config.Relay.BroadcastAudio(frame)
}

// interleaveChannels converts de-interleaved channels to interleaved.
// Input: [[L0,L1,L2], [R0,R1,R2]] → Output: [L0,R0,L1,R1,L2,R2]
func interleaveChannels(channels [][]float32) []float32 {
	if len(channels) == 0 {
		return nil
	}
	numCh := len(channels)
	samplesPerCh := len(channels[0])
	result := make([]float32, samplesPerCh*numCh)
	for i := 0; i < samplesPerCh; i++ {
		for ch := 0; ch < numCh; ch++ {
			if i < len(channels[ch]) {
				result[i*numCh+ch] = channels[ch][i]
			}
		}
	}
	return result
}
