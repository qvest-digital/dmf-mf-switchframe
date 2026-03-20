// Package output provides MPEG-TS muxing and output adapters for
// Switchframe's recording and SRT streaming pipeline.
package output

import (
	"bytes"
	"context"
	"reflect"
	"sync"
	"unsafe"

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
	// pmtPID is the MPEG-TS packet identifier for the Program Map Table.
	// This matches the go-astits default PMT PID.
	pmtPID uint16 = 0x1000
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
	mu            sync.Mutex
	muxer         *astits.Muxer
	buf           *bytes.Buffer
	output        func([]byte)
	initialized   bool
	cancel        context.CancelFunc
	pendingAudio  []*media.AudioFrame
	annexBBuf     []byte
	prependBuf    []byte
	scte35PID     uint16 // 0 = disabled; non-zero = enabled with this PID
	pendingSCTE35 [][]byte
	lastVideoPTS  int64
	lastPCRPTS    int64
	scte35CC      uint8 // continuity counter for SCTE-35 PID

	// Muxer-owned clock: PTS assigned from monotonic counters, not inherited
	// from upstream. Both video and audio derive from the same epoch, so
	// A/V sync is correct by construction.
	muxerEpoch      int64 // starting PTS (90000 = 1 second)
	videoFrameCount int64 // incremented on each WriteVideo
	audioFrameCount int64 // incremented on each WriteAudio
	videoFrameDur   int64 // 90kHz ticks per video frame (e.g., 3750 for 24fps)
	audioFrameDur   int64 // 90kHz ticks per audio frame (1920 for 48kHz/1024)
	lipSyncOffset   int64 // 90kHz ticks added to video PTS (positive = delay video)
}

// NewTSMuxer creates an uninitialized TSMuxer. Call SetOutput before
// writing frames. The muxer initializes on the first keyframe.
func NewTSMuxer() *TSMuxer {
	return &TSMuxer{
		annexBBuf:     make([]byte, 0, muxerBufCap),
		prependBuf:    make([]byte, 0, muxerBufCap),
		muxerEpoch:    90000, // start at 1 second
		videoFrameDur: 3750,  // default 24fps (90000/24)
		audioFrameDur: 1920,  // 48kHz / 1024 samples per AAC frame
		lipSyncOffset: 5760,  // ~64ms at 90kHz (3 audio frames) — delays video PTS
		                      // to compensate for video arriving at muxer before audio
	}
}

// SetVideoFrameRate sets the video frame duration for the muxer-owned clock.
// Must be called before writing frames. fpsNum/fpsDen express the frame rate
// as a rational number (e.g., 24/1 for 24fps, 30000/1001 for 29.97fps).
func (m *TSMuxer) SetVideoFrameRate(fpsNum, fpsDen int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if fpsNum > 0 && fpsDen > 0 {
		m.videoFrameDur = int64(90000) * int64(fpsDen) / int64(fpsNum)
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

// ResetCounters resets the frame counters to zero and forces the muxer to
// re-initialize on the next keyframe. Called when a new SRT client connects
// so the first data it receives has PTS near zero — otherwise SRT's TSBPD
// would buffer packets for the accumulated PTS duration (potentially minutes).
func (m *TSMuxer) ResetCounters() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.videoFrameCount = 0
	m.audioFrameCount = 0
	m.lastPCRPTS = 0
	m.lastVideoPTS = 0
	// Force re-init so the next keyframe triggers PAT/PMT + fresh start.
	if m.muxer != nil {
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		m.muxer = nil
	}
	m.initialized = false
}

// SetLipSyncOffset sets the lip-sync adjustment in milliseconds.
// Positive values delay video relative to audio (use when video is
// ahead of audio). Negative values advance video.
func (m *TSMuxer) SetLipSyncOffset(ms int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lipSyncOffset = int64(ms) * 90 // ms → 90kHz ticks
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
// SCTE-35 section 8.1 in the PMT program_info loop. The format identifier
// 0x43554549 corresponds to the ASCII string "CUEI".
func cueiDescriptor() *astits.Descriptor {
	return &astits.Descriptor{
		Tag:    astits.DescriptorTagRegistration,
		Length: 4, // format_identifier is 4 bytes; go-astits skips body if Length==0
		Registration: &astits.DescriptorRegistration{
			FormatIdentifier: 0x43554549, // "CUEI"
		},
	}
}

// setProgramDescriptors sets PMT program-level descriptors on the go-astits
// Muxer. go-astits v1.15.0 stores PMT data in the private `pmt` field of
// type PMTData. PMTData.ProgramDescriptors is properly encoded by
// writePMTSection, but no public API exposes it. This function uses reflect
// to access the private field. Pinned to go-astits v1.15.0 via go.mod.
//
// Must be called after AddElementaryStream and before WriteTables.
func setProgramDescriptors(muxer *astits.Muxer, descriptors []*astits.Descriptor) {
	v := reflect.ValueOf(muxer).Elem()
	pmtField := v.FieldByName("pmt")
	pdField := pmtField.FieldByName("ProgramDescriptors")
	// ProgramDescriptors ([]*Descriptor) is exported, but lives inside the
	// unexported `pmt` field — reflect won't let us Set() it directly.
	*(*[]*astits.Descriptor)(unsafe.Pointer(pdField.UnsafeAddr())) = descriptors
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
		if err := m.init(frame.PTS); err != nil {
			return err
		}
	}

	// Muxer-owned clock: assign PTS from monotonic frame counter.
	// Upstream PTS is ignored — both video and audio derive from the
	// same epoch, so A/V sync is correct by construction.
	muxerPTS := (m.muxerEpoch + m.videoFrameCount*m.videoFrameDur + m.lipSyncOffset) & 0x1FFFFFFFF
	m.videoFrameCount++
	m.lastVideoPTS = muxerPTS

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
	ptsRef := &astits.ClockReference{Base: muxerPTS}
	// No B-frames: DTS always equals PTS.
	ptsDTSIndicator := uint8(astits.PTSDTSIndicatorOnlyPTS)
	var dtsRef *astits.ClockReference

	af := &astits.PacketAdaptationField{
		RandomAccessIndicator: frame.IsKeyframe,
	}

	// Insert PCR on keyframes and at least every 30ms (under the 40ms MPEG-TS requirement).
	// PCR base uses the same 90kHz timebase as PTS.
	const pcrInterval = 2700 // 30ms at 90kHz — ensures PCR on every frame at 30fps (33.3ms > 30ms)
	// Use wrap-aware comparison: PTS is a 33-bit field in MPEG-TS and wraps
	// from 2^33-1 back to 0. Masking the subtraction to 33 bits ensures the
	// delta is always positive and forward-looking across the wrap boundary.
	if frame.IsKeyframe || (muxerPTS-m.lastPCRPTS)&0x1FFFFFFFF >= pcrInterval {
		af.HasPCR = true
		af.PCR = &astits.ClockReference{Base: muxerPTS}
		m.lastPCRPTS = muxerPTS
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
		// Drop audio before muxer is initialized (waiting for first keyframe).
		return nil
	}

	// Muxer-owned clock: assign PTS from monotonic frame counter.
	muxerPTS := (m.muxerEpoch + m.audioFrameCount*m.audioFrameDur) & 0x1FFFFFFFF
	m.audioFrameCount++

	// Ensure ADTS header is present.
	data := codec.EnsureADTS(frame.Data, frame.SampleRate, frame.Channels)

	ptsRef := &astits.ClockReference{Base: muxerPTS}

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
	m.lastPCRPTS = 0
	m.videoFrameCount = 0
	m.audioFrameCount = 0
	return nil
}

// init creates the go-astits muxer and registers elementary streams.
// Must be called with m.mu held.
func (m *TSMuxer) init(_ int64) error {
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
			ElementaryPID: m.scte35PID,
			StreamType:    astits.StreamType(0x86), // SCTE-35
		}); err != nil {
			cancel()
			return err
		}

		// Place CUEI registration descriptor in the PMT program_info loop
		// per SCTE-35 section 8.1 ("shall be included in the program_info
		// loop of the TS_program_map_section"). go-astits has no public API
		// for program-level descriptors, so we use reflect to set the
		// private pmt.ProgramDescriptors field.
		setProgramDescriptors(m.muxer, []*astits.Descriptor{cueiDescriptor()})
	}

	// Set video PID as the PCR source.
	m.muxer.SetPCRPID(videoPID)

	// Write initial PAT/PMT tables.
	if _, err := m.muxer.WriteTables(); err != nil {
		cancel()
		return err
	}

	m.initialized = true

	// Discard all pending audio. Audio that arrived before the first video
	// keyframe is stale — writing it creates a PTS gap where the player
	// plays audio before any video appears, causing A/V desync. Fresh
	// audio will arrive via WriteAudio after init returns.
	m.pendingAudio = nil

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
// Design note: rather than using a sync.Pool for flush buffers,
// we pass m.buf.Bytes() directly
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
