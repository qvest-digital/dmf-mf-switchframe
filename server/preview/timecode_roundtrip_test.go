package preview

import (
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/codec"
)

// TestEncoder_TimecodePassthroughRoundTrip exercises the full switchframe-side
// timecode passthrough chain: SendWithTimecode -> ffenc_open_preview's
// pic-struct=1 (sets pic_struct_present_flag in the SPS) -> x264 emits an empty
// pic_timing SEI shell -> rewriteAVC1WithTimecode replaces it with the source
// timecode. It then parses the pic_timing SEI back out of the encoded AVC1 wire
// data exactly as the operator UI's timecode.js does, and asserts the SMPTE
// timecode round-trips. (ffprobe independently confirms the same output decodes
// to 11:48:54:16; this in-process reader keeps the test dependency-free.)
func TestEncoder_TimecodePassthroughRoundTrip(t *testing.T) {
	skipWithoutEncoder(t)

	const w, h = 640, 360
	relay := &mockRelay{}
	enc, err := NewEncoder(Config{
		SourceKey: "tc-roundtrip",
		Width:     w,
		Height:    h,
		Bitrate:   800_000,
		FPSNum:    30,
		FPSDen:    1,
		Relay:     relay,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	// Raw AV_FRAME_DATA_S12M_TIMECODE side data for 11:48:54:16 (the file.ts
	// golden fixture): little-endian count=1, packed word 0x16544811.
	tcBytes := []byte{0x01, 0x00, 0x00, 0x00, 0x11, 0x48, 0x54, 0x16}
	for i := 0; i < 16; i++ {
		yuv := makeYUV420(w, h, byte(16+i))
		enc.SendWithTimecode(yuv, w, h, int64(i)*3000, tcBytes)
		time.Sleep(20 * time.Millisecond)
	}
	enc.Stop() // drains and flushes the encode goroutine

	frames := relay.getVideos()
	if len(frames) == 0 {
		t.Fatal("no encoded frames produced")
	}

	found := false
	for _, f := range frames {
		hh, mm, ss, ff, ok := readPicTimingTimecode(f.WireData)
		if !ok {
			continue
		}
		found = true
		if hh != 11 || mm != 48 || ss != 54 || ff != 16 {
			t.Errorf("round-trip timecode = %02d:%02d:%02d:%02d, want 11:48:54:16", hh, mm, ss, ff)
		}
	}
	if !found {
		t.Fatal("no readable pic_timing SEI with a populated timecode found in any encoded frame")
	}
}

// TestFFmpegDecoder_SurfacesPassthroughTimecode guards the decode->capture link
// that the proxy relies on: feedmonitor reads the source timecode via
// codec.FFmpegDecoder.LastTimecodeBytes() after each Decode and forwards it to
// the encoder. The round-trip test above hardcodes the timecode and never
// touches the decoder, so it cannot catch a decoder that fails to capture the
// S12M side data (which is exactly how passthrough silently shipped broken).
// Here we re-encode timecode-bearing frames, then decode the wire output through
// the real FFmpegDecoder and assert LastTimecodeBytes() surfaces 11:48:54:16.
func TestFFmpegDecoder_SurfacesPassthroughTimecode(t *testing.T) {
	skipWithoutEncoder(t)

	const w, h = 640, 360
	relay := &mockRelay{}
	enc, err := NewEncoder(Config{
		SourceKey: "tc-decode",
		Width:     w,
		Height:    h,
		Bitrate:   800_000,
		FPSNum:    30,
		FPSDen:    1,
		Relay:     relay,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	tcBytes := []byte{0x01, 0x00, 0x00, 0x00, 0x11, 0x48, 0x54, 0x16} // 11:48:54:16
	for i := 0; i < 16; i++ {
		enc.SendWithTimecode(makeYUV420(w, h, byte(16+i)), w, h, int64(i)*3000, tcBytes)
		time.Sleep(20 * time.Millisecond)
	}
	enc.Stop()

	frames := relay.getVideos()
	if len(frames) == 0 {
		t.Fatal("no encoded frames produced")
	}

	dec, err := codec.NewFFmpegDecoder(nil)
	if err != nil {
		t.Fatalf("NewFFmpegDecoder: %v", err)
	}
	defer dec.Close()

	found := false
	for _, f := range frames {
		if _, _, _, derr := dec.Decode(codec.AVC1ToAnnexB(f.WireData)); derr != nil {
			continue // warmup buffering
		}
		tc := dec.LastTimecodeBytes()
		if len(tc) == 0 {
			continue
		}
		parsed, ok := caption.FromS12MBytes(tc)
		if !ok {
			continue
		}
		found = true
		if parsed.Hours != 11 || parsed.Minutes != 48 || parsed.Seconds != 54 || parsed.Frames != 16 {
			t.Errorf("decoder LastTimecodeBytes = %02d:%02d:%02d:%02d, want 11:48:54:16",
				parsed.Hours, parsed.Minutes, parsed.Seconds, parsed.Frames)
		}
	}
	if !found {
		t.Fatal("FFmpegDecoder.LastTimecodeBytes never surfaced the source timecode (decode->capture link broken)")
	}
}

// readPicTimingTimecode walks the NALUs of an AVC1 access unit, finds the
// pic_timing SEI (payload type 1) with a populated clock_timestamp, and decodes
// the SMPTE timecode — matching the SPS shape ffenc_open_preview produces
// (pic_struct_present_flag=1, no HRD). Mirrors what the browser front end reads.
func readPicTimingTimecode(avc1 []byte) (hh, mm, ss, ff int, ok bool) {
	for _, n := range codec.ExtractNALUs(avc1) {
		if len(n) < 2 || (n[0]&0x1F) != 6 { // SEI NAL only
			continue
		}
		rbsp := stripEPB(n[1:]) // SEI RBSP after the 1-byte NAL header
		pos := 0
		readVal := func() int { // SEI payload_type / payload_size: ff-extended
			v := 0
			for pos < len(rbsp) {
				b := int(rbsp[pos])
				pos++
				v += b
				if b != 0xFF {
					break
				}
			}
			return v
		}
		for pos < len(rbsp) {
			ptype := readVal()
			psize := readVal()
			if pos+psize > len(rbsp) {
				break
			}
			payload := rbsp[pos : pos+psize]
			pos += psize
			if ptype != 1 { // pic_timing only
				continue
			}
			br := bitReader{buf: payload}
			br.read(4)           // pic_struct (NumClockTS=1 for 0)
			if br.read(1) != 1 { // clock_timestamp_flag[0]
				continue
			}
			br.read(2)         // ct_type
			br.read(1)         // nuit_field_based_flag
			br.read(5)         // counting_type
			full := br.read(1) // full_timestamp_flag
			br.read(1)         // discontinuity_flag
			br.read(1)         // cnt_dropped_flag
			ff = br.read(8)    // n_frames
			if full == 1 {
				ss = br.read(6)
				mm = br.read(6)
				hh = br.read(5)
			}
			return hh, mm, ss, ff, true
		}
	}
	return 0, 0, 0, 0, false
}

// stripEPB removes H.264 emulation-prevention bytes (0x03 in a 0x00 0x00 0x03
// sequence) from an RBSP.
func stripEPB(b []byte) []byte {
	out := make([]byte, 0, len(b))
	zeros := 0
	for i := 0; i < len(b); i++ {
		if zeros >= 2 && b[i] == 0x03 && i+1 < len(b) && b[i+1] <= 0x03 {
			zeros = 0
			continue // drop emulation_prevention_three_byte
		}
		if b[i] == 0 {
			zeros++
		} else {
			zeros = 0
		}
		out = append(out, b[i])
	}
	return out
}

// bitReader reads MSB-first; out-of-range reads return 0.
type bitReader struct {
	buf []byte
	pos int // bit position
}

func (r *bitReader) read(n int) int {
	v := 0
	for i := 0; i < n; i++ {
		bytePos := r.pos >> 3
		if bytePos >= len(r.buf) {
			return v << (n - i - 1)
		}
		bit := (int(r.buf[bytePos]) >> (7 - (r.pos & 7))) & 1
		v = (v << 1) | bit
		r.pos++
	}
	return v
}
