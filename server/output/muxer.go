// Package output provides MPEG-TS muxing and output adapters for
// Switchframe's recording and SRT streaming pipeline.
package output

import (
	"bytes"
	"context"
	"sync"

	astits "github.com/asticode/go-astits"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
)

const (
	// videoPID is the MPEG-TS packet identifier for the H.264 video stream.
	videoPID uint16 = 0x100
	// audioPID is the MPEG-TS packet identifier for the AAC audio stream.
	audioPID uint16 = 0x101
)

// TSMuxer wraps go-astits to produce 188-byte MPEG-TS packets from
// Prism video and audio frames. Initialization is deferred until the
// first keyframe arrives, since SPS/PPS are needed for PAT/PMT.
type TSMuxer struct {
	mu          sync.Mutex
	muxer       *astits.Muxer
	buf         *bytes.Buffer
	output      func([]byte)
	initialized bool
	cancel      context.CancelFunc
}

// NewTSMuxer creates an uninitialized TSMuxer. Call SetOutput before
// writing frames. The muxer initializes on the first keyframe.
func NewTSMuxer() *TSMuxer {
	return &TSMuxer{}
}

// SetOutput sets the callback that receives muxed MPEG-TS data.
// The callback is invoked after each frame is written, with data
// that is always a multiple of 188 bytes.
func (m *TSMuxer) SetOutput(fn func([]byte)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.output = fn
}

// WriteVideo muxes a video frame into MPEG-TS packets. If the muxer is
// not yet initialized, non-keyframes are silently dropped. The first
// keyframe triggers initialization (PAT/PMT with codec config).
//
// On keyframes, SPS and PPS are prepended as Annex B NALUs before the
// frame data.
func (m *TSMuxer) WriteVideo(frame *media.VideoFrame) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.initialized {
		if !frame.IsKeyframe {
			return nil
		}
		if err := m.init(); err != nil {
			return err
		}
	}

	// Convert AVC1 wire data to Annex B format.
	annexB := codec.AVC1ToAnnexB(frame.WireData)
	if len(annexB) == 0 {
		return nil
	}

	// On keyframes, prepend SPS + PPS as Annex B NALUs.
	if frame.IsKeyframe {
		var prefix []byte
		if len(frame.SPS) > 0 {
			prefix = append(prefix, 0x00, 0x00, 0x00, 0x01)
			prefix = append(prefix, frame.SPS...)
		}
		if len(frame.PPS) > 0 {
			prefix = append(prefix, 0x00, 0x00, 0x00, 0x01)
			prefix = append(prefix, frame.PPS...)
		}
		if len(prefix) > 0 {
			annexB = append(prefix, annexB...)
		}
	}

	// Build PES data for video.
	ptsRef := &astits.ClockReference{Base: frame.PTS}
	dtsRef := &astits.ClockReference{Base: frame.DTS}

	ptsDTSIndicator := uint8(astits.PTSDTSIndicatorBothPresent)
	if frame.PTS == frame.DTS {
		ptsDTSIndicator = uint8(astits.PTSDTSIndicatorOnlyPTS)
		dtsRef = nil
	}

	af := &astits.PacketAdaptationField{
		RandomAccessIndicator: frame.IsKeyframe,
	}

	md := &astits.MuxerData{
		PID:             videoPID,
		AdaptationField: af,
		PES: &astits.PESData{
			Header: &astits.PESHeader{
				PacketLength: 0, // Unbounded for video.
				StreamID:     0xE0,
				OptionalHeader: &astits.PESOptionalHeader{
					PTSDTSIndicator: ptsDTSIndicator,
					PTS:             ptsRef,
					DTS:             dtsRef,
				},
			},
			Data: annexB,
		},
	}

	if _, err := m.muxer.WriteData(md); err != nil {
		return err
	}

	return m.flush()
}

// WriteAudio muxes an audio frame into MPEG-TS packets. Frames
// received before the muxer is initialized are silently dropped.
//
// Audio data gets an ADTS header if one is not already present.
func (m *TSMuxer) WriteAudio(frame *media.AudioFrame) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.initialized {
		return nil
	}

	// Ensure ADTS header is present.
	data := codec.EnsureADTS(frame.Data, frame.SampleRate, frame.Channels)

	ptsRef := &astits.ClockReference{Base: frame.PTS}

	md := &astits.MuxerData{
		PID: audioPID,
		PES: &astits.PESData{
			Header: &astits.PESHeader{
				StreamID: 0xC0,
				OptionalHeader: &astits.PESOptionalHeader{
					PTSDTSIndicator: uint8(astits.PTSDTSIndicatorOnlyPTS),
					PTS:             ptsRef,
				},
			},
			Data: data,
		},
	}

	if _, err := m.muxer.WriteData(md); err != nil {
		return err
	}

	return m.flush()
}

// Close releases muxer resources. Safe to call multiple times.
func (m *TSMuxer) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.muxer = nil
	m.initialized = false
	return nil
}

// init creates the go-astits muxer and registers elementary streams.
// Must be called with m.mu held.
func (m *TSMuxer) init() error {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	m.buf = &bytes.Buffer{}
	m.muxer = astits.NewMuxer(ctx, m.buf)

	// Register video elementary stream (H.264).
	if err := m.muxer.AddElementaryStream(astits.PMTElementaryStream{
		ElementaryPID: videoPID,
		StreamType:    astits.StreamTypeH264Video,
	}); err != nil {
		cancel()
		return err
	}

	// Register audio elementary stream (AAC with ADTS).
	if err := m.muxer.AddElementaryStream(astits.PMTElementaryStream{
		ElementaryPID: audioPID,
		StreamType:    astits.StreamTypeADTS,
	}); err != nil {
		cancel()
		return err
	}

	// Set video PID as the PCR source.
	m.muxer.SetPCRPID(videoPID)

	// Write initial PAT/PMT tables.
	if _, err := m.muxer.WriteTables(); err != nil {
		cancel()
		return err
	}

	m.initialized = true

	// PAT/PMT remain in the buffer and will be flushed alongside the first keyframe.
	return nil
}

// flush sends buffered TS data to the output callback and resets the
// buffer. Must be called with m.mu held.
func (m *TSMuxer) flush() error {
	if m.buf == nil || m.buf.Len() == 0 {
		return nil
	}
	if m.output != nil {
		data := make([]byte, m.buf.Len())
		copy(data, m.buf.Bytes())
		m.output(data)
	}
	m.buf.Reset()
	return nil
}
