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
	muxerBufCap = 65536 // 64KB default buffer capacity for Annex B buffers
	// videoPID is the MPEG-TS packet identifier for the H.264 video stream.
	videoPID uint16 = 0x100
	// audioPID is the MPEG-TS packet identifier for the AAC audio stream.
	audioPID uint16 = 0x101
	// defaultSCTE35PID is the default MPEG-TS packet identifier for the SCTE-35 cue stream.
	// Used by tests and as the fallback when no PID is explicitly configured.
	defaultSCTE35PID uint16 = 0x102
	// maxPendingAudio is the maximum number of audio frames buffered
	// before the muxer is initialized (first keyframe). At 48kHz with
	// 1024-sample AAC frames, 50 frames ≈ ~1 second of audio.
	maxPendingAudio = 50
	// maxPendingSCTE35 is the maximum number of SCTE-35 sections buffered
	// before the muxer is initialized (first keyframe).
	maxPendingSCTE35 = 10
)

// TSMuxer wraps go-astits to produce 188-byte MPEG-TS packets from
// Prism video and audio frames. Initialization is deferred until the
// first keyframe arrives, since SPS/PPS are needed for PAT/PMT.
type TSMuxer struct {
	mu             sync.Mutex
	muxer          *astits.Muxer
	buf            *bytes.Buffer
	output         func([]byte)
	initialized    bool
	cancel         context.CancelFunc
	pendingAudio   []*media.AudioFrame
	annexBBuf      []byte
	prependBuf     []byte
	scte35PID      uint16 // 0 = disabled; non-zero = enabled with this PID
	pendingSCTE35  [][]byte
	lastVideoPTS   int64
	scte35CC       uint8 // continuity counter for SCTE-35 PID
}

// NewTSMuxer creates an uninitialized TSMuxer. Call SetOutput before
// writing frames. The muxer initializes on the first keyframe.
func NewTSMuxer() *TSMuxer {
	return &TSMuxer{
		annexBBuf:  make([]byte, 0, muxerBufCap),
		prependBuf: make([]byte, 0, muxerBufCap),
	}
}

// SetOutput sets the callback that receives muxed MPEG-TS data.
// The callback is invoked after each frame is written, with data
// that is always a multiple of 188 bytes.
func (m *TSMuxer) SetOutput(fn func([]byte)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.output = fn
}

// SetSCTE35PID configures the SCTE-35 PID for this muxer. A non-zero PID
// enables SCTE-35 support; zero disables it. When enabled, the PMT will
// include a SCTE-35 elementary stream with a CUEI registration descriptor.
// Must be called before the first keyframe.
func (m *TSMuxer) SetSCTE35PID(pid uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scte35PID = pid
}

// CurrentPTS returns the PTS of the most recently written video frame.
// Returns 0 if no video frames have been written yet.
func (m *TSMuxer) CurrentPTS() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastVideoPTS
}

// WriteSCTE35 writes a SCTE-35 section to the MPEG-TS stream. The data
// should be a complete splice_info_section (table_id 0xFC). If SCTE-35
// is not enabled, the call is silently ignored. If the muxer has not
// been initialized yet (no keyframe received), the section is buffered
// and flushed when the first keyframe arrives.
func (m *TSMuxer) WriteSCTE35(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.scte35PID == 0 {
		return nil
	}

	if !m.initialized {
		// Buffer SCTE-35 sections until the muxer is initialized.
		dataCopy := make([]byte, len(data))
		copy(dataCopy, data)
		m.pendingSCTE35 = append(m.pendingSCTE35, dataCopy)
		if len(m.pendingSCTE35) > maxPendingSCTE35 {
			m.pendingSCTE35 = m.pendingSCTE35[len(m.pendingSCTE35)-maxPendingSCTE35:]
		}
		return nil
	}

	return m.writeSCTE35Locked(data)
}

// writeSCTE35Locked constructs raw 188-byte TS packets for a SCTE-35
// section. SCTE-35 sections are PSI (Program Specific Information),
// not PES, so they use a pointer_field rather than PES headers.
// Must be called with m.mu held.
func (m *TSMuxer) writeSCTE35Locked(data []byte) error {
	// SCTE-35 sections are typically small (< 183 bytes), but we handle
	// multi-packet spanning for correctness.
	remaining := data
	first := true

	for len(remaining) > 0 {
		var pkt [tsPacketSize]byte

		// Sync byte
		pkt[0] = 0x47

		// PID (13 bits) with payload_unit_start_indicator
		if first {
			pkt[1] = 0x40 | byte(m.scte35PID>>8) // PUSI=1 + PID high bits
		} else {
			pkt[1] = byte(m.scte35PID >> 8) // PUSI=0 + PID high bits
		}
		pkt[2] = byte(m.scte35PID & 0xFF)

		// Adaptation field control (0x10 = payload only) + continuity counter
		pkt[3] = 0x10 | (m.scte35CC & 0x0F)
		m.scte35CC++

		headerLen := 4
		payloadStart := headerLen

		if first {
			// Pointer field: 0x00 means section starts immediately after
			pkt[payloadStart] = 0x00
			payloadStart++
			first = false
		}

		payloadSpace := tsPacketSize - payloadStart
		n := len(remaining)
		if n > payloadSpace {
			n = payloadSpace
		}
		copy(pkt[payloadStart:], remaining[:n])
		remaining = remaining[n:]

		// Pad remainder with 0xFF (stuffing bytes)
		for i := payloadStart + n; i < tsPacketSize; i++ {
			pkt[i] = 0xFF
		}

		if _, err := m.buf.Write(pkt[:]); err != nil {
			return err
		}
	}

	return m.flush()
}

// cueiDescriptor builds the CUEI registration descriptor required by
// SCTE-35 for PMT elementary stream entries. The format identifier
// 0x43554549 corresponds to the ASCII string "CUEI".
//
// Spec note: SCTE-35 section 8.1 specifies that the CUEI registration
// descriptor should appear in the PMT program_info loop (program-level),
// not in the ES_info loop. However, go-astits only exposes descriptors
// via PMTElementaryStream.ElementaryStreamDescriptors (ES-level); the
// PMT.ProgramInfoDescriptors field is not publicly settable. ES_info
// placement is widely accepted by downstream equipment and decoders.
func cueiDescriptor() *astits.Descriptor {
	return &astits.Descriptor{
		Tag: astits.DescriptorTagRegistration,
		Registration: &astits.DescriptorRegistration{
			FormatIdentifier: 0x43554549, // "CUEI"
		},
	}
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

	m.lastVideoPTS = frame.PTS

	// Convert AVC1 wire data to Annex B format, reusing the buffer.
	m.annexBBuf = codec.AVC1ToAnnexBInto(frame.WireData, m.annexBBuf[:0])
	if len(m.annexBBuf) == 0 {
		return nil
	}

	annexB := m.annexBBuf
	// On keyframes, prepend SPS + PPS as Annex B NALUs.
	if frame.IsKeyframe {
		m.prependBuf = codec.PrependSPSPPSInto(frame.SPS, frame.PPS, m.annexBBuf, m.prependBuf[:0])
		annexB = m.prependBuf
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
// received before the muxer is initialized are buffered (up to
// maxPendingAudio) and flushed when the first keyframe arrives.
//
// Audio data gets an ADTS header if one is not already present.
func (m *TSMuxer) WriteAudio(frame *media.AudioFrame) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.initialized {
		// Buffer audio frames before muxer initialization so they
		// can be flushed when the first keyframe arrives, preventing
		// AV sync drift at recording start.
		m.pendingAudio = append(m.pendingAudio, frame)
		if len(m.pendingAudio) > maxPendingAudio {
			m.pendingAudio = m.pendingAudio[len(m.pendingAudio)-maxPendingAudio:]
		}
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
	m.pendingAudio = nil
	m.pendingSCTE35 = nil
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

	// Conditionally register SCTE-35 elementary stream.
	if m.scte35PID != 0 {
		if err := m.muxer.AddElementaryStream(astits.PMTElementaryStream{
			ElementaryPID:               m.scte35PID,
			StreamType:                  astits.StreamType(0x86), // SCTE-35
			ElementaryStreamDescriptors: []*astits.Descriptor{cueiDescriptor()},
		}); err != nil {
			cancel()
			return err
		}
	}

	// Set video PID as the PCR source.
	m.muxer.SetPCRPID(videoPID)

	// Write initial PAT/PMT tables.
	if _, err := m.muxer.WriteTables(); err != nil {
		cancel()
		return err
	}

	m.initialized = true

	// Flush any audio frames that were buffered before initialization.
	// These are written now so they appear in the output before the
	// first video keyframe, preventing AV sync drift at recording start.
	if len(m.pendingAudio) > 0 {
		for _, af := range m.pendingAudio {
			data := codec.EnsureADTS(af.Data, af.SampleRate, af.Channels)
			ptsRef := &astits.ClockReference{Base: af.PTS}
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
				m.pendingAudio = nil
				return err
			}
		}
		m.pendingAudio = nil
	}

	// Flush any SCTE-35 sections that were buffered before initialization.
	if len(m.pendingSCTE35) > 0 {
		for _, data := range m.pendingSCTE35 {
			if err := m.writeSCTE35Locked(data); err != nil {
				m.pendingSCTE35 = nil
				return err
			}
		}
		m.pendingSCTE35 = nil
	}

	// PAT/PMT + buffered audio remain in the buffer and will be flushed
	// alongside the first keyframe.
	return nil
}

// flush sends buffered TS data to the output callback and resets the
// buffer. Must be called with m.mu held.
//
// Design note: rather than using a sync.Pool for flush buffers (as
// originally planned in Phase 22 WG1.5), we pass m.buf.Bytes() directly
// to the output callback. This is a zero-copy optimization — the slice
// is valid only during the synchronous callback, and AsyncAdapter.Write
// copies the data into its own pooled buffer. This eliminates both the
// flush allocation AND the pool overhead.
func (m *TSMuxer) flush() error {
	if m.buf == nil || m.buf.Len() == 0 {
		return nil
	}
	if m.output != nil {
		m.output(m.buf.Bytes())
	}
	m.buf.Reset()
	return nil
}
