package preview

import (
	"encoding/binary"

	"github.com/zsiec/switchframe/server/codec"
)

// rewriteAVC1WithTimecode replaces x264's empty Pic Timing SEI in an
// AVC1-formatted access unit with one populated from the supplied
// AV_FRAME_DATA_S12M_TIMECODE side-data bytes.
//
// FFmpeg 8's libavcodec/libx264.c wrapper sets up pic_struct_present_flag
// in the SPS (via our `x264-params pic-struct=1`) and asks x264 to emit
// Pic Timing SEI, but it does NOT forward AV_FRAME_DATA_S12M_TIMECODE
// side data into x264's pic.extra_sei. The result is a 1-byte SEI payload
// with clock_timestamp_flag=0 — structurally well-formed, but missing the
// HH:MM:SS:FF the front end is supposed to read.
//
// We patch that up here: walk the NALUs, find the SEI containing a payload
// of type 1 (pic_timing), replace that NALU with one we construct from the
// S12M_TIMECODE bytes. The rest of the bitstream is untouched. Returns the
// original buffer unchanged when tcBytes is empty or no Pic Timing SEI
// exists in the input (e.g. on the OpenH264 fallback path which doesn't
// emit Pic Timing at all).
func rewriteAVC1WithTimecode(avc1, tcBytes []byte) []byte {
	if len(tcBytes) < 8 || len(avc1) == 0 {
		return avc1
	}
	newSEI := buildPicTimingSEINALU(tcBytes)
	if len(newSEI) == 0 {
		return avc1
	}

	nalus := codec.ExtractNALUs(avc1)
	if len(nalus) == 0 {
		return avc1
	}

	replaced := false
	out := make([][]byte, 0, len(nalus))
	for _, n := range nalus {
		// SEI NALU containing a Pic Timing message (payload type 1) is the
		// one we replace. The version-string SEI (type 5) and any future
		// SEI types stay untouched.
		if !replaced && len(n) >= 2 && (n[0]&0x1F) == 6 && n[1] == 0x01 {
			out = append(out, newSEI)
			replaced = true
			continue
		}
		out = append(out, n)
	}
	if !replaced {
		return avc1
	}
	return repackAVC1(out)
}

// repackAVC1 concatenates NALUs into the 4-byte length-prefixed AVC1 wire
// format. Caller owns the result; no aliasing with inputs.
func repackAVC1(nalus [][]byte) []byte {
	total := 0
	for _, n := range nalus {
		total += 4 + len(n)
	}
	out := make([]byte, total)
	pos := 0
	for _, n := range nalus {
		binary.BigEndian.PutUint32(out[pos:pos+4], uint32(len(n)))
		copy(out[pos+4:pos+4+len(n)], n)
		pos += 4 + len(n)
	}
	return out
}

// buildPicTimingSEINALU constructs a complete H.264 SEI NALU containing a
// single Pic Timing message with one populated clock_timestamp built from
// AV_FRAME_DATA_S12M_TIMECODE side data.
//
// The side data layout is four little-endian uint32s: tc[0] = number of
// valid timecodes (1-3); tc[1..3] = packed SMPTE timecodes per
// av_timecode_get_smpte (BCD-packed hours/minutes/seconds/frames with a
// drop-frame flag at bit 30). We only emit the first one — Pic Timing
// supports up to three but in practice every broadcast feed uses one.
//
// The output assumes our encoder's SPS shape: pic_struct_present_flag=1,
// no HRD parameters. That matches what ffenc_open_preview produces.
func buildPicTimingSEINALU(tcBytes []byte) []byte {
	if len(tcBytes) < 8 {
		return nil
	}
	count := binary.LittleEndian.Uint32(tcBytes[0:4])
	if count == 0 {
		return nil
	}
	payload := buildPicTimingPayload(tcBytes[4:8])
	if len(payload) == 0 {
		return nil
	}

	// SEI message: payload_type byte(s), payload_size byte(s), payload.
	// Type 1 fits in one byte. Size up to 254 fits in one byte; for ≥ 255
	// we'd emit 0xFF-prefixed bytes. Pic Timing for our shape is ~6 bytes,
	// so the single-byte size path always applies in practice.
	size := len(payload)
	var sizeBytes []byte
	for size >= 255 {
		sizeBytes = append(sizeBytes, 0xFF)
		size -= 255
	}
	sizeBytes = append(sizeBytes, byte(size))

	nalu := make([]byte, 0, 2+len(sizeBytes)+len(payload)+1)
	nalu = append(nalu, 0x06)         // NAL header: nal_unit_type=6 (SEI)
	nalu = append(nalu, 0x01)         // payload_type=1 (pic_timing)
	nalu = append(nalu, sizeBytes...) // payload_size
	nalu = append(nalu, payload...)
	nalu = append(nalu, 0x80) // rbsp_trailing_bits: 1 + zero-pad to byte
	return nalu
}

// buildPicTimingPayload bit-packs a Pic Timing SEI payload from one packed
// S12M timecode word. Layout (when SPS has pic_struct_present_flag=1 and no
// HRD parameters):
//
//	pic_struct                (4 bits, 0 = progressive frame)
//	for each clock_ts (=1):
//	  clock_timestamp_flag    (1 bit, 1 = present)
//	  ct_type                 (2 bits, 0)
//	  nuit_field_based_flag   (1 bit, 0)
//	  counting_type           (5 bits, 0)
//	  full_timestamp_flag     (1 bit, 1 = hh+mm+ss inline)
//	  discontinuity_flag      (1 bit, 0)
//	  cnt_dropped_flag        (1 bit, from drop-frame flag)
//	  n_frames                (8 bits)
//	  seconds_value           (6 bits)
//	  minutes_value           (6 bits)
//	  hours_value             (5 bits)
//	  time_offset             (0 bits, timeOffsetLen=0)
//
// 41 payload bits + trailing `1` + zero-pad to byte boundary = 48 bits = 6 bytes.
// The payload itself is byte-aligned per spec (sei_payload's internal trailing
// bits); the enclosing SEI NALU adds its own rbsp_trailing_bits after.
//
// S12M packed-word layout per FFmpeg's av_timecode_get_smpte (libavutil 8.x):
//
//	bits 0-3:   hh units (BCD)
//	bits 4-5:   hh tens  (BCD, 2 bits)
//	bits 6-7:   FIELD/BGF (PAL 50p odd-frame indicator at bit 7; else 0)
//	bits 8-11:  mm units
//	bits 12-14: mm tens  (3 bits)
//	bit  15:    BGF0 / BGF2 (always 0 from FFmpeg's packer)
//	bits 16-19: ss units
//	bits 20-22: ss tens  (3 bits)
//	bit  23:    FIELD (NTSC >30p odd-frame indicator; toggles per frame at 60p/59.94p)
//	bits 24-27: ff units
//	bits 28-29: ff tens  (2 bits)
//	bit  30:    drop-frame flag
//	bit  31:    color-frame flag (always 0 from FFmpeg's packer)
//
// The tens-field masks below intentionally mirror those widths exactly so the
// FIELD/BGF bits aren't OR'd into the BCD digit — that was an earlier bug that
// made the displayed seconds appear to spin twice per source frame for 60p/
// 59.94p NTSC sources (bit 23 alternates → ssTens picks up the extra `8`).
func buildPicTimingPayload(tcLE []byte) []byte {
	if len(tcLE) < 4 {
		return nil
	}
	tc := binary.LittleEndian.Uint32(tcLE)

	dropFrame := (tc >> 30) & 1
	ffTens := (tc >> 28) & 0x3
	ffUnits := (tc >> 24) & 0xF
	nFrames := ffTens*10 + ffUnits
	ssTens := (tc >> 20) & 0x7
	ssUnits := (tc >> 16) & 0xF
	seconds := ssTens*10 + ssUnits
	mmTens := (tc >> 12) & 0x7
	mmUnits := (tc >> 8) & 0xF
	minutes := mmTens*10 + mmUnits
	hhTens := (tc >> 4) & 0x3
	hhUnits := tc & 0xF
	hours := hhTens*10 + hhUnits

	bw := bitWriter{}
	bw.write(0, 4)         // pic_struct
	bw.write(1, 1)         // clock_timestamp_flag[0]
	bw.write(0, 2)         // ct_type
	bw.write(0, 1)         // nuit_field_based_flag
	bw.write(0, 5)         // counting_type
	bw.write(1, 1)         // full_timestamp_flag
	bw.write(0, 1)         // discontinuity_flag
	bw.write(dropFrame, 1) // cnt_dropped_flag
	bw.write(nFrames, 8)
	bw.write(seconds, 6)
	bw.write(minutes, 6)
	bw.write(hours, 5)
	// sei_payload internal trailing: `1` + zero-pad to byte boundary.
	bw.write(1, 1)
	bw.alignToByte()
	return bw.buf
}

// bitWriter packs MSB-first into an expanding byte slice. Internal use only.
type bitWriter struct {
	buf []byte
	bit int // 0..7, next bit position within the current byte
}

func (b *bitWriter) write(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		bit := byte((v >> uint(i)) & 1)
		if b.bit == 0 {
			b.buf = append(b.buf, 0)
		}
		b.buf[len(b.buf)-1] |= bit << (7 - b.bit)
		b.bit++
		if b.bit == 8 {
			b.bit = 0
		}
	}
}

// alignToByte rounds the next write position up to the next byte boundary.
// The current byte's remaining bits stay zero (the caller wrote `1` before
// this, so this is the zero-pad portion of rbsp_trailing_bits).
func (b *bitWriter) alignToByte() {
	if b.bit > 0 {
		b.bit = 0
	}
}
