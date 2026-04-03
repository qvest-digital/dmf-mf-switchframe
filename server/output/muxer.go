// Package output provides MPEG-TS muxing and output adapters for
// Switchframe's recording and SRT streaming pipeline.
package output

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"sync"
	"unsafe"

	astits "github.com/asticode/go-astits"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/internal/mpegts"
)

const (
	muxerBufCap = 65536 // 64KB default buffer capacity for Annex B buffers

	// maxPendingSCTE35 is the maximum number of SCTE-35 sections buffered
	// before the muxer is initialized (first keyframe).
	maxPendingSCTE35 = 10

	// maxPendingIDRSCTE35 is the maximum number of SCTE-35 sections that
	// can be buffered waiting for an IDR-aligned flush after initialization.
	maxPendingIDRSCTE35 = 4

	// idrAlignTimeoutFrames is the number of video frames to wait for an
	// IDR before flushing pending SCTE-35 sections regardless. At 30fps
	// with a 2s GOP, this covers ~2 GOPs to ensure sections are not delayed
	// indefinitely.
	idrAlignTimeoutFrames = 120
)

// TSMuxer wraps go-astits to produce 188-byte MPEG-TS packets from
// Prism video and audio frames. Initialization is deferred until the
// first keyframe arrives. The video codec (AVC or HEVC) is detected
// from the first keyframe's Codec field and determines the PMT stream
// type and parameter set prepending behavior.
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
	videoCodec    string // "h264" (default) or "h265"; set from first keyframe's Codec field
	scte35PID     uint16 // 0 = disabled; non-zero = enabled with this PID
	pendingSCTE35 [][]byte

	// IDR-aligned SCTE-35 buffering: after initialization, WriteSCTE35
	// buffers sections here and WriteVideo flushes them on the next keyframe.
	// This ensures SCTE-35 cues appear immediately before the IDR they
	// reference, which is required for clean splice points.
	pendingIDRSCTE35   [][]byte
	idrAlignFrameCount int // frames since last IDR-aligned SCTE-35 was buffered

	lastVideoPTS int64
	lastPCRPTS   int64
	scte35CC     uint8 // continuity counter for SCTE-35 PID

	// PTS rebasing: upstream wall-clock PTS is rebased to near-zero for
	// SRT TSBPD compatibility. Both video and audio use the same base.
	muxerEpoch int64 // starting PTS after rebase (default 90000 = 1 second)
	ptsBase    int64 // first video frame's upstream PTS — subtracted from all PTS
	ptsBaseSet bool  // true after first video frame sets the base

	// Lip-sync compensation: video PTS is delayed by this amount to
	// compensate for the FIFO latency in the audio ring buffer. The video
	// path uses newest-wins (zero content latency) while the audio path
	// uses a FIFO ring buffer (content delayed by buffer depth). Without
	// compensation, video content appears ahead of audio content.
	lipSyncOffset int64 // 90kHz ticks added to video PTS only

	// Diagnostics: track upstream PTS values and rebased output PTS
	// to identify A/V sync drift.
	lastUpstreamVideoPTS int64 // upstream PTS before rebasing
	lastUpstreamAudioPTS int64 // upstream PTS before rebasing
	lastMuxedVideoPTS    int64 // PTS written to TS (after rebase + lip-sync)
	lastMuxedAudioPTS    int64 // PTS written to TS (after rebase)
	diagCounter          int   // for periodic logging
	maxBitrate           int   // encoder max bitrate in bits/sec (for PMT descriptor); 0 = use default 5 Mbps
	diagPrevMuxerPTS     int64 // PTS delta diagnostic: previous muxerPTS for delta tracking
}

// NewTSMuxer creates an uninitialized TSMuxer. Call SetOutput before
// writing frames. The muxer initializes on the first keyframe.
func NewTSMuxer() *TSMuxer {
	return &TSMuxer{
		annexBBuf:  make([]byte, 0, muxerBufCap),
		prependBuf: make([]byte, 0, muxerBufCap),
		muxerEpoch: 90000, // start at 1 second after rebase
	}
}

// SetOutput sets the callback that receives muxed MPEG-TS data.
func (m *TSMuxer) SetOutput(fn func([]byte)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.output = fn
}

// SetLipSyncOffset90k sets the lip-sync compensation offset in 90kHz ticks.
// This value is added to video PTS only, delaying video presentation relative
// to audio to compensate for the audio ring buffer's FIFO latency.
// Thread-safe — can be called concurrently with WriteVideo/WriteAudio.
func (m *TSMuxer) SetLipSyncOffset90k(ticks int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lipSyncOffset = ticks
}

// LipSyncOffset90k returns the current lip-sync offset in 90kHz ticks.
func (m *TSMuxer) LipSyncOffset90k() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lipSyncOffset
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

// maxBitrateBytes returns the max bitrate in bytes/sec for the PMT descriptor.
func (m *TSMuxer) maxBitrateBytes() uint32 {
	bps := m.maxBitrate
	if bps <= 0 {
		bps = 50000000 // default 50 Mbps
	}
	return uint32(bps / 8)
}

// SetMaxBitrate sets the video encoder's maximum bitrate (bits/sec) for
// the PMT maximum_bitrate descriptor. Must be called before the first keyframe.
func (m *TSMuxer) SetMaxBitrate(bps int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxBitrate = bps
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

	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	if !m.initialized {
		// Buffer SCTE-35 sections until the muxer is initialized.
		m.pendingSCTE35 = append(m.pendingSCTE35, dataCopy)
		if len(m.pendingSCTE35) > maxPendingSCTE35 {
			m.pendingSCTE35 = m.pendingSCTE35[len(m.pendingSCTE35)-maxPendingSCTE35:]
		}
		return nil
	}

	// Buffer for IDR-aligned insertion. The section will be flushed
	// immediately before the next keyframe in WriteVideo, ensuring
	// SCTE-35 cues are co-located with the IDR they reference.
	m.pendingIDRSCTE35 = append(m.pendingIDRSCTE35, dataCopy)
	if len(m.pendingIDRSCTE35) > maxPendingIDRSCTE35 {
		m.pendingIDRSCTE35 = m.pendingIDRSCTE35[len(m.pendingIDRSCTE35)-maxPendingIDRSCTE35:]
	}
	m.idrAlignFrameCount = 0
	return nil
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
		var pkt [mpegts.PacketSize]byte

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

		payloadSpace := mpegts.PacketSize - payloadStart
		n := len(remaining)
		if n > payloadSpace {
			n = payloadSpace
		}
		copy(pkt[payloadStart:], remaining[:n])
		remaining = remaining[n:]

		// Pad remainder with 0xFF (stuffing bytes)
		for i := payloadStart + n; i < mpegts.PacketSize; i++ {
			pkt[i] = 0xFF
		}

		if _, err := m.buf.Write(pkt[:]); err != nil {
			return err
		}
	}

	return m.flush()
}

// buildSDTPacket constructs a single 188-byte MPEG-TS packet containing a
// Service Description Table (SDT) with the Switchframe service name and
// provider. This makes the stream identifiable in tools like MediaInfo,
// ffprobe, and broadcast analyzers.
//
// SDT structure per ISO 13818-1 / ETSI EN 300 468:
//
//	PID 0x0011, table_id 0x42 (SDT actual), service descriptor tag 0x48.
func buildSDTPacket() []byte {
	serviceName := []byte("Switchframe")
	providerName := []byte("switchframe.dev")

	// Service descriptor (tag 0x48): type + provider_len + provider + name_len + name
	descPayload := make([]byte, 0, 3+len(providerName)+len(serviceName))
	descPayload = append(descPayload, 0x48) // descriptor_tag
	descLen := 1 + 1 + len(providerName) + 1 + len(serviceName)
	descPayload = append(descPayload, byte(descLen))           // descriptor_length
	descPayload = append(descPayload, 0x01)                    // service_type: digital television
	descPayload = append(descPayload, byte(len(providerName))) // provider name length
	descPayload = append(descPayload, providerName...)
	descPayload = append(descPayload, byte(len(serviceName))) // service name length
	descPayload = append(descPayload, serviceName...)

	// Service loop entry: service_id(2) + flags(1) + desc_loop_length(2) + descriptors
	descLoopLen := len(descPayload)
	serviceEntry := make([]byte, 0, 5+descLoopLen)
	serviceEntry = append(serviceEntry, 0x00, 0x01)                                            // service_id = 1
	serviceEntry = append(serviceEntry, 0xFC)                                                  // reserved(6) + EIT_schedule(0) + EIT_pf(0)
	serviceEntry = append(serviceEntry, byte(0x80|((descLoopLen>>8)&0x0F)), byte(descLoopLen)) // running_status=4(running), free_CA=0, desc_loop_length
	serviceEntry = append(serviceEntry, descPayload...)

	// SDT section header (before CRC):
	//   table_id(1) + flags+length(2) + ts_id(2) + version(1) + section_num(1) +
	//   last_section_num(1) + original_network_id(2) + reserved(1) + service_entries
	sectionBody := make([]byte, 0, 8+len(serviceEntry))
	sectionBody = append(sectionBody, 0x00, 0x00) // transport_stream_id = 0 (matches go-astits PAT default)
	sectionBody = append(sectionBody, 0xC1)       // reserved(2)=0b11, version(5)=0, current_next=1
	sectionBody = append(sectionBody, 0x00)       // section_number
	sectionBody = append(sectionBody, 0x00)       // last_section_number
	sectionBody = append(sectionBody, 0x00, 0x00) // original_network_id = 0
	sectionBody = append(sectionBody, 0xFF)       // reserved_future_use
	sectionBody = append(sectionBody, serviceEntry...)

	// Full section: table_id + section_syntax_indicator + length + body + CRC32
	sectionLen := len(sectionBody) + 4 // +4 for CRC32
	section := make([]byte, 0, 3+len(sectionBody)+4)
	section = append(section, mpegts.TableIDSDT)                                   // table_id: SDT actual
	section = append(section, byte(0xF0|((sectionLen>>8)&0x0F)), byte(sectionLen)) // section_syntax_indicator=1, reserved=0b11, section_length
	section = append(section, sectionBody...)

	// CRC-32/MPEG-2
	crc := mpegts.CRC32MPEG2(section)
	section = append(section, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))

	// Wrap in TS packet
	pkt := make([]byte, mpegts.PacketSize)
	pkt[0] = mpegts.SyncByte                       // sync byte
	pkt[1] = 0x40 | byte((mpegts.SDTPID>>8)&0x1F) // payload_unit_start=1, PID high
	pkt[2] = byte(mpegts.SDTPID & 0xFF)            // PID low
	pkt[3] = 0x10                                   // adaptation_field_control=01 (payload only), cc=0
	pkt[4] = 0x00                                   // pointer_field
	copy(pkt[5:], section)
	for i := 5 + len(section); i < mpegts.PacketSize; i++ {
		pkt[i] = 0xFF // stuffing
	}

	return pkt
}

// buildNITPacket constructs a single 188-byte MPEG-TS packet containing a
// Network Information Table (NIT) with the Switchframe network name.
// MediaInfo displays the NIT network_name in the General section, making
// "Switchframe" visible at the top of the file info.
//
// NIT structure per ISO 13818-1 / ETSI EN 300 468:
//
//	PID 0x0010, table_id 0x40 (NIT actual), network_name_descriptor tag 0x40.
func buildNITPacket() []byte {
	networkName := []byte("Switchframe")

	// Network name descriptor (tag 0x40)
	nameDesc := make([]byte, 0, 2+len(networkName))
	nameDesc = append(nameDesc, 0x40)                   // descriptor_tag: network_name
	nameDesc = append(nameDesc, byte(len(networkName))) // descriptor_length
	nameDesc = append(nameDesc, networkName...)

	// NIT section body (after table_id + flags+length):
	//   network_id(2) + version(1) + section_num(1) + last_section_num(1) +
	//   reserved+network_descriptors_length(2) + descriptors +
	//   reserved+transport_stream_loop_length(2) [empty loop]
	descLen := len(nameDesc)
	sectionBody := make([]byte, 0, 7+descLen+2)
	sectionBody = append(sectionBody, 0x00, 0x01) // network_id = 1
	sectionBody = append(sectionBody, 0xC1)       // reserved(2)=0b11, version(5)=0, current_next=1
	sectionBody = append(sectionBody, 0x00)       // section_number
	sectionBody = append(sectionBody, 0x00)       // last_section_number
	// reserved(4) + network_descriptors_length(12)
	sectionBody = append(sectionBody, byte(0xF0|((descLen>>8)&0x0F)), byte(descLen))
	sectionBody = append(sectionBody, nameDesc...)
	// reserved(4) + transport_stream_loop_length(12) = 0 (no TS loop entries)
	sectionBody = append(sectionBody, 0xF0, 0x00)

	// Full section: table_id + section_syntax_indicator + length + body + CRC32
	sectionLen := len(sectionBody) + 4 // +4 for CRC32
	section := make([]byte, 0, 3+len(sectionBody)+4)
	section = append(section, mpegts.TableIDNIT)                                   // table_id: NIT actual
	section = append(section, byte(0xF0|((sectionLen>>8)&0x0F)), byte(sectionLen)) // section_syntax_indicator=1, reserved, section_length
	section = append(section, sectionBody...)

	// CRC-32/MPEG-2
	crc := mpegts.CRC32MPEG2(section)
	section = append(section, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))

	// Wrap in TS packet
	pkt := make([]byte, mpegts.PacketSize)
	pkt[0] = mpegts.SyncByte                       // sync byte
	pkt[1] = 0x40 | byte((mpegts.NITPID>>8)&0x1F) // payload_unit_start=1, PID high
	pkt[2] = byte(mpegts.NITPID & 0xFF)            // PID low
	pkt[3] = 0x10                                   // adaptation_field_control=01 (payload only), cc=0
	pkt[4] = 0x00                                   // pointer_field
	copy(pkt[5:], section)
	for i := 5 + len(section); i < mpegts.PacketSize; i++ {
		pkt[i] = 0xFF // stuffing
	}

	return pkt
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
// On keyframes, parameter sets are prepended as Annex B NALUs before the
// frame data: SPS+PPS for AVC, VPS+SPS+PPS for HEVC.
func (m *TSMuxer) WriteVideo(frame *media.VideoFrame) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.initialized {
		if !frame.IsKeyframe {
			return nil
		}
		// Detect codec from first keyframe.
		if frame.Codec == "h265" {
			m.videoCodec = "h265"
		} else {
			m.videoCodec = "h264"
		}
		if err := m.init(frame.PTS); err != nil {
			return err
		}
	}

	// Use upstream PTS (wall-clock domain) rebased to near-zero.
	// Both video and audio use the same wall-clock PTS system (seeded from
	// SeedPTSFromVideo), so their PTS reflects the actual source content
	// timing. Rebasing keeps values small for SRT TSBPD compatibility.
	if !m.ptsBaseSet {
		m.ptsBase = frame.PTS
		m.ptsBaseSet = true
	}
	// Add lip-sync offset to VIDEO PTS only. This delays video
	// presentation for SRT/recording output to compensate for the audio
	// ring buffer's FIFO latency. The browser path handles its own
	// compensation in audio-decoder.ts getPlaybackPTS().
	muxerPTS := (frame.PTS - m.ptsBase + m.muxerEpoch + m.lipSyncOffset) & 0x1FFFFFFFF
	// Diagnostic: verify muxerPTS deltas are nominal.
	if prev := m.diagPrevMuxerPTS; prev != 0 {
		delta := (muxerPTS - prev) & 0x1FFFFFFFF
		if !isNominalPTSDelta(delta) {
			slog.Warn("muxer: non-nominal muxerPTS delta",
				"delta", delta, "muxerPTS", muxerPTS, "prevMuxerPTS", prev,
				"upstreamPTS", frame.PTS, "lipSyncOffset", m.lipSyncOffset)
		}
	}
	m.diagPrevMuxerPTS = muxerPTS
	m.lastVideoPTS = muxerPTS
	m.lastUpstreamVideoPTS = frame.PTS
	m.lastMuxedVideoPTS = muxerPTS

	// Periodic A/V sync diagnostics: log PTS values every ~5s.
	m.diagCounter++
	if m.diagCounter%150 == 1 && m.lastUpstreamAudioPTS != 0 {
		upstreamGap := m.lastUpstreamVideoPTS - m.lastUpstreamAudioPTS
		muxedGap := m.lastMuxedVideoPTS - m.lastMuxedAudioPTS
		slog.Info("muxer A/V PTS diagnostic",
			"upstream_video", m.lastUpstreamVideoPTS,
			"upstream_audio", m.lastUpstreamAudioPTS,
			"upstream_gap_90k", upstreamGap,
			"upstream_gap_ms", float64(upstreamGap)/90.0,
			"muxed_video", m.lastMuxedVideoPTS,
			"muxed_audio", m.lastMuxedAudioPTS,
			"muxed_gap_90k", muxedGap,
			"muxed_gap_ms", float64(muxedGap)/90.0,
			"lip_sync_90k", m.lipSyncOffset,
			"lip_sync_ms", float64(m.lipSyncOffset)/90.0,
		)
	}

	// Flush pending IDR-aligned SCTE-35 sections. On keyframes, flush
	// immediately before the IDR so the cue and keyframe are co-located
	// in the TS stream. If no keyframe arrives within idrAlignTimeoutFrames,
	// flush anyway to avoid indefinite delay.
	if len(m.pendingIDRSCTE35) > 0 {
		m.idrAlignFrameCount++
		if frame.IsKeyframe || m.idrAlignFrameCount >= idrAlignTimeoutFrames {
			for _, section := range m.pendingIDRSCTE35 {
				if err := m.writeSCTE35Locked(section); err != nil {
					return err
				}
			}
			m.pendingIDRSCTE35 = m.pendingIDRSCTE35[:0]
			m.idrAlignFrameCount = 0
		}
	}

	// Convert wire data (4-byte length-prefixed NALUs) to Annex B format.
	// The wire format is identical for AVC and HEVC.
	m.annexBBuf = codec.WireToAnnexBInto(frame.WireData, m.annexBBuf[:0])
	if len(m.annexBBuf) == 0 {
		return nil
	}

	annexB := m.annexBBuf
	// On keyframes, prepend parameter sets as Annex B NALUs.
	if frame.IsKeyframe {
		if m.videoCodec == "h265" {
			// HEVC: prepend VPS + SPS + PPS.
			m.prependBuf = codec.PrependVPSSPSPPSInto(frame.VPS, frame.SPS, frame.PPS, m.annexBBuf, m.prependBuf[:0])
		} else {
			// AVC: prepend SPS + PPS.
			m.prependBuf = codec.PrependSPSPPSInto(frame.SPS, frame.PPS, m.annexBBuf, m.prependBuf[:0])
		}
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
		PID:             mpegts.VideoPID,
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

// isNominalPTSDelta returns true if delta matches a standard frame duration
// in 90kHz ticks (±1 tick tolerance for rounding).
func isNominalPTSDelta(delta int64) bool {
	switch delta {
	case 3000: // 30fps
		return true
	case 3003, 3004: // 29.97fps (30000/1001)
		return true
	case 3600: // 25fps
		return true
	case 3750: // 24fps
		return true
	case 3753, 3754: // 23.976fps (24000/1001)
		return true
	case 1800: // 50fps
		return true
	case 1500: // 60fps
		return true
	case 1501, 1502: // 59.94fps (60000/1001)
		return true
	default:
		return false
	}
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

	// Use upstream PTS rebased to near-zero (same base as video).
	muxerPTS := (frame.PTS - m.ptsBase + m.muxerEpoch) & 0x1FFFFFFFF
	m.lastUpstreamAudioPTS = frame.PTS
	m.lastMuxedAudioPTS = muxerPTS

	// Ensure ADTS header is present.
	data := codec.EnsureADTS(frame.Data, frame.SampleRate, frame.Channels)

	ptsRef := &astits.ClockReference{Base: muxerPTS}

	md := &astits.MuxerData{
		PID: mpegts.AudioPID,
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
	m.videoCodec = ""
	m.pendingAudio = nil
	m.pendingSCTE35 = nil
	m.pendingIDRSCTE35 = nil
	m.idrAlignFrameCount = 0
	m.lastPCRPTS = 0
	m.ptsBase = 0
	m.ptsBaseSet = false
	return nil
}

// init creates the go-astits muxer and registers elementary streams.
// Must be called with m.mu held. The videoCodec field must be set before calling.
func (m *TSMuxer) init(_ int64) error {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	m.buf = &bytes.Buffer{}
	m.muxer = astits.NewMuxer(ctx, m.buf)

	// Register video elementary stream with codec-appropriate stream type
	// and ES-level descriptors for broadcast metadata.
	videoStreamType := astits.StreamTypeH264Video
	if m.videoCodec == "h265" {
		videoStreamType = astits.StreamTypeH265Video
	}
	if err := m.muxer.AddElementaryStream(astits.PMTElementaryStream{
		ElementaryPID: mpegts.VideoPID,
		StreamType:    videoStreamType,
		ElementaryStreamDescriptors: []*astits.Descriptor{
			{
				Tag:            astits.DescriptorTagMaximumBitrate,
				Length:         3, // 22-bit field in 3 bytes
				MaximumBitrate: &astits.DescriptorMaximumBitrate{Bitrate: m.maxBitrateBytes()},
			},
			{
				Tag:                 astits.DescriptorTagDataStreamAlignment,
				Length:              1,
				DataStreamAlignment: &astits.DescriptorDataStreamAlignment{Type: uint8(astits.DataStreamAligmentVideoAccessUnit)},
			},
		},
	}); err != nil {
		cancel()
		return err
	}

	// Register audio elementary stream (AAC with ADTS) with language and alignment.
	if err := m.muxer.AddElementaryStream(astits.PMTElementaryStream{
		ElementaryPID: mpegts.AudioPID,
		StreamType:    astits.StreamTypeADTS,
		ElementaryStreamDescriptors: []*astits.Descriptor{
			{
				Tag:                        astits.DescriptorTagISO639LanguageAndAudioType,
				Length:                     4, // 3-byte language code + 1-byte audio_type
				ISO639LanguageAndAudioType: &astits.DescriptorISO639LanguageAndAudioType{Language: []byte("eng"), Type: 0},
			},
			{
				Tag:                 astits.DescriptorTagDataStreamAlignment,
				Length:              1,
				DataStreamAlignment: &astits.DescriptorDataStreamAlignment{Type: uint8(astits.DataStreamAligmentAudioSyncWord)},
			},
		},
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
	m.muxer.SetPCRPID(mpegts.VideoPID)

	// Write initial PAT/PMT tables.
	if _, err := m.muxer.WriteTables(); err != nil {
		cancel()
		return err
	}

	// Write NIT with Switchframe network name (appears in MediaInfo General section).
	if _, err := m.buf.Write(buildNITPacket()); err != nil {
		cancel()
		return err
	}

	// Write SDT with Switchframe service name (appears in MediaInfo Menu section).
	if _, err := m.buf.Write(buildSDTPacket()); err != nil {
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
