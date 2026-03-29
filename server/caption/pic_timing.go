package caption

// PicTimingConfig holds SPS-derived parameters needed for pic_timing SEI generation.
//
// pic_timing is a mandatory SEI message for broadcast-compliant streams.
// Downstream decoders (Evertz, Harmonic, etc.) require pic_timing to precede
// user_data_registered_itu_t_t35 (caption SEI) in the same access unit.
type PicTimingConfig struct {
	// From SPS VUI: nal_hrd_parameters_present_flag.
	NalHRDParametersPresent bool

	// From SPS VUI: vcl_hrd_parameters_present_flag.
	VclHRDParametersPresent bool

	// From SPS VUI: pic_struct_present_flag (H.264) or frame_field_info_present_flag (HEVC).
	PicStructPresent bool

	// From HRD parameters: cpb_removal_delay_length_minus1 + 1.
	// Typically 24 for NVENC CBR/VBR. Only used when NalHRD or VclHRD present.
	CpbRemovalDelayLength int

	// From HRD parameters: dpb_output_delay_length_minus1 + 1.
	// Typically 24 for NVENC CBR/VBR. Only used when NalHRD or VclHRD present.
	DpbOutputDelayLength int

	// Codec type: true for HEVC, false for H.264.
	IsHEVC bool

	// HEVC-specific: frame_field_info_present_flag from VPS/SPS.
	FrameFieldInfoPresent bool

	// HEVC-specific: CpbDpbDelaysPresentFlag derived from VPS HRD.
	CpbDpbDelaysPresent bool
}

// DefaultPicTimingConfig returns a PicTimingConfig matching typical NVENC CBR/VBR
// output with HRD parameters present. This is the safest default for broadcast
// compatibility — NVENC and x264 with nal-hrd=cbr both produce these values.
func DefaultPicTimingConfig() PicTimingConfig {
	return PicTimingConfig{
		NalHRDParametersPresent: true,
		VclHRDParametersPresent: false,
		PicStructPresent:        true,
		CpbRemovalDelayLength:   24,
		DpbOutputDelayLength:    24,
	}
}

// DefaultHEVCPicTimingConfig returns a PicTimingConfig for HEVC streams.
func DefaultHEVCPicTimingConfig() PicTimingConfig {
	return PicTimingConfig{
		IsHEVC:                true,
		FrameFieldInfoPresent: true,
		CpbDpbDelaysPresent:   true,
		CpbRemovalDelayLength: 24,
		DpbOutputDelayLength:  24,
	}
}

// hrdPresent returns true if either NAL or VCL HRD parameters are present.
func (c PicTimingConfig) hrdPresent() bool {
	return c.NalHRDParametersPresent || c.VclHRDParametersPresent
}

// seiPayloadTypePicTiming is the SEI payload type for pic_timing.
const seiPayloadTypePicTiming = 1

// BuildPicTimingSEI generates a pic_timing SEI payload (payload_type=1).
//
// For H.264 (ITU-T H.264 D.2.2):
//   - If CpbDpbDelaysPresentFlag (hrdPresent): cpb_removal_delay + dpb_output_delay
//   - If pic_struct_present_flag: pic_struct(4 bits) + zero clock timestamps
//   - Padded to byte boundary
//
// For HEVC (ITU-T H.265 D.2.3):
//   - If frame_field_info_present_flag: pic_struct(4) + source_scan_type(2) + duplicate_flag(1)
//   - If CpbDpbDelaysPresentFlag: au_cpb_removal_delay_minus1 + pic_dpb_output_delay
//   - Padded to byte boundary
//
// frameNum is the monotonically increasing frame counter used for cpb_removal_delay.
func BuildPicTimingSEI(cfg PicTimingConfig, frameNum int64) []byte {
	if cfg.IsHEVC {
		return buildPicTimingSEIHEVC(cfg, frameNum)
	}
	return buildPicTimingSEIH264(cfg, frameNum)
}

// buildPicTimingSEIH264 builds an H.264 pic_timing SEI payload.
func buildPicTimingSEIH264(cfg PicTimingConfig, frameNum int64) []byte {
	var bw bitWriter
	bw.init(16) // generous initial capacity

	// ITU-T H.264 D.2.2:
	// If CpbDpbDelaysPresentFlag (derived from NAL/VCL HRD params present):
	//   cpb_removal_delay: u(cpb_removal_delay_length)
	//   dpb_output_delay:  u(dpb_output_delay_length)
	if cfg.hrdPresent() {
		// cpb_removal_delay: monotonically increasing, frameNum * 2 clock ticks.
		// (Each frame = 2 clock ticks in the 2*fps clock domain.)
		cpbDelay := uint64(frameNum * 2)
		bw.writeBits(cpbDelay, cfg.CpbRemovalDelayLength)

		// dpb_output_delay: 0 for non-B-frame progressive streams.
		bw.writeBits(0, cfg.DpbOutputDelayLength)
	}

	// If pic_struct_present_flag:
	//   pic_struct: u(4)
	//   Then for i = 0..NumClockTS-1: clock_timestamp_flag[i] u(1)
	//   NumClockTS is derived from pic_struct via Table D-1.
	//   pic_struct=0 (progressive frame) → NumClockTS=1
	if cfg.PicStructPresent {
		// pic_struct = 0: progressive frame.
		bw.writeBits(0, 4)
		// NumClockTS = 1 for pic_struct=0. Write clock_timestamp_flag[0] = 0.
		bw.writeBits(0, 1)
	}

	// Pad to byte boundary with trailing bits (1 + zeros).
	bw.byteAlign()

	return bw.bytes()
}

// buildPicTimingSEIHEVC builds an HEVC pic_timing SEI payload.
func buildPicTimingSEIHEVC(cfg PicTimingConfig, frameNum int64) []byte {
	var bw bitWriter
	bw.init(16)

	// ITU-T H.265 D.2.3:
	// If frame_field_info_present_flag:
	//   pic_struct: u(4)
	//   source_scan_type: u(2)
	//   duplicate_flag: u(1)
	if cfg.FrameFieldInfoPresent {
		// pic_struct = 0: progressive frame.
		bw.writeBits(0, 4)
		// source_scan_type = 1: progressive (interlaced=0, progressive=1, unknown=2, reserved=3).
		bw.writeBits(1, 2)
		// duplicate_flag = 0: not a duplicate.
		bw.writeBits(0, 1)
	}

	// If CpbDpbDelaysPresentFlag:
	//   au_cpb_removal_delay_minus1: u(au_cpb_removal_delay_length_minus1 + 1)
	//   pic_dpb_output_delay: u(dpb_output_delay_length_minus1 + 1)
	if cfg.CpbDpbDelaysPresent {
		// au_cpb_removal_delay_minus1: frameNum * 2 - 1 (but min 0 for frame 0).
		delay := uint64(frameNum * 2)
		if delay > 0 {
			delay--
		}
		bw.writeBits(delay, cfg.CpbRemovalDelayLength)

		// pic_dpb_output_delay: 0 for progressive non-B-frame.
		bw.writeBits(0, cfg.DpbOutputDelayLength)
	}

	// Pad to byte boundary.
	bw.byteAlign()

	return bw.bytes()
}

// BuildPicTimingAndCaptionSEI builds a single SEI NALU containing both
// pic_timing (payload_type=1) and user_data_registered_itu_t_t35 (payload_type=4).
//
// Multiple SEI messages can coexist in a single SEI NALU — they are concatenated
// before the RBSP trailing bits. This is per ITU-T H.264 7.3.2.3.1 / H.265 7.3.2.4.
//
// The pic_timing message MUST come before the caption message in the NALU for
// broadcast decoder compliance.
//
// codec: "h264" or "" for H.264, "h265" for HEVC.
func BuildPicTimingAndCaptionSEI(pairs []CCPair, ptCfg PicTimingConfig, frameNum int64, codec string) []byte {
	// Always emit at least a null pair to maintain continuous cc_data presence
	// in the stream. This signals caption carriage to downstream decoders from
	// the first frame, regardless of whether text is being authored.
	if len(pairs) == 0 {
		pairs = []CCPair{NullPair()}
	}

	isHEVC := codec == "h265"

	// Clamp pairs like BuildSEINALU.
	if len(pairs) > 15 {
		pairs = pairs[:15]
	}

	// Build pic_timing payload.
	ptPayload := BuildPicTimingSEI(ptCfg, frameNum)

	// Build caption payload (the raw SEI message payload, same as in BuildSEINALU).
	ccCount := len(pairs) * 2
	captionPayloadSize := 1 + 2 + 4 + 1 + 1 + 1 + 3*ccCount + 1

	// NALU header size.
	naluHeaderSize := 1
	if isHEVC {
		naluHeaderSize = 2
	}

	// Total buffer: start_code(4) + nalu_header + pic_timing_msg + caption_msg + trailing(1)
	// pic_timing_msg = type(1) + size(variable) + payload
	// caption_msg = type(1) + size(variable) + payload
	ptMsgSize := 1 + seiSizeLen(len(ptPayload)) + len(ptPayload)
	capMsgSize := 1 + seiSizeLen(captionPayloadSize) + captionPayloadSize

	buf := make([]byte, 0, 4+naluHeaderSize+ptMsgSize+capMsgSize+1+capMsgSize/2)

	// Annex B start code.
	buf = append(buf, annexBSC0, annexBSC1, annexBSC2, annexBSC3)

	// NALU header.
	if isHEVC {
		buf = append(buf, hevcSEIByte0, hevcSEIByte1)
	} else {
		buf = append(buf, naluTypeSEI)
	}

	// --- SEI message 1: pic_timing (payload_type=1) ---
	buf = append(buf, seiPayloadTypePicTiming)
	buf = appendSEISize(buf, len(ptPayload))
	payloadStart := len(buf) // EPB scan starts here
	buf = append(buf, ptPayload...)

	// --- SEI message 2: user_data_registered_itu_t_t35 (payload_type=4) ---
	buf = append(buf, seiPayloadTypeT35)
	buf = appendSEISize(buf, captionPayloadSize)
	buf = appendCaptionPayload(buf, pairs, ccCount)

	// RBSP trailing bits.
	buf = append(buf, 0x80)

	// Insert emulation prevention bytes.
	buf = insertEPB(buf, payloadStart)

	return buf
}

// appendCaptionPayload appends the ATSC A/53 caption payload to buf.
// This is the same payload content as BuildSEINALU's inner body.
func appendCaptionPayload(buf []byte, pairs []CCPair, ccCount int) []byte {
	// country_code = 0xB5 (United States)
	buf = append(buf, seiCountryCode)

	// provider_code = 0x0031 (ATSC)
	buf = append(buf, byte(seiProviderCode>>8), byte(seiProviderCode))

	// user_identifier = "GA94"
	buf = append(buf, seiUserID0, seiUserID1, seiUserID2, seiUserID3)

	// user_data_type_code = 0x03 (cc_data)
	buf = append(buf, seiCCDataType)

	// process_em_data_flag(1) | process_cc_data_flag(1) | additional_data_flag(0) | cc_count(5)
	ccCountByte := byte(0xC0) | byte(ccCount&0x1F)
	buf = append(buf, ccCountByte)

	// em_data byte (required by ATSC A/53, must be 0xFF).
	buf = append(buf, 0xFF)

	// cc_data_pkt triples.
	for _, pair := range pairs {
		buf = append(buf, 0xFC, pair.Data[0], pair.Data[1])
		buf = append(buf, 0xF9, 0x80, 0x80)
	}

	// Marker byte 0xFF.
	buf = append(buf, 0xFF)

	return buf
}

// seiSizeLen returns the number of bytes needed to encode a SEI payload size.
func seiSizeLen(size int) int {
	if size < 255 {
		return 1
	}
	n := size / 255
	return n + 1
}

// appendSEISize appends a SEI payload size encoding to buf.
func appendSEISize(buf []byte, size int) []byte {
	for size >= 255 {
		buf = append(buf, 0xFF)
		size -= 255
	}
	buf = append(buf, byte(size))
	return buf
}

// bitWriter is a simple bit-level writer for constructing SEI payloads.
type bitWriter struct {
	buf     []byte
	bitPos  int // bits written into current byte (0-7)
	current byte
}

func (w *bitWriter) init(cap int) {
	w.buf = make([]byte, 0, cap)
}

// writeBits writes the lowest nBits of val (big-endian, MSB first).
func (w *bitWriter) writeBits(val uint64, nBits int) {
	for i := nBits - 1; i >= 0; i-- {
		bit := (val >> uint(i)) & 1
		w.current |= byte(bit) << uint(7-w.bitPos)
		w.bitPos++
		if w.bitPos == 8 {
			w.buf = append(w.buf, w.current)
			w.current = 0
			w.bitPos = 0
		}
	}
}

// byteAlign pads with zero bits to the next byte boundary.
// If already aligned, does nothing.
func (w *bitWriter) byteAlign() {
	if w.bitPos > 0 {
		w.buf = append(w.buf, w.current)
		w.current = 0
		w.bitPos = 0
	}
}

// bytes returns the accumulated bytes.
func (w *bitWriter) bytes() []byte {
	// Flush any partial byte.
	if w.bitPos > 0 {
		w.buf = append(w.buf, w.current)
		w.current = 0
		w.bitPos = 0
	}
	return w.buf
}
