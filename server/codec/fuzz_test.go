package codec

import (
	"encoding/binary"
	"testing"
)

// ---------- NALU fuzz targets ----------

func FuzzAVC1ToAnnexB(f *testing.F) {
	// Seed: valid AVC1 with one IDR NALU (4-byte length prefix + body).
	nalu := []byte{0x65, 0x01, 0x02, 0x03}
	seed := make([]byte, 4+len(nalu))
	binary.BigEndian.PutUint32(seed[:4], uint32(len(nalu)))
	copy(seed[4:], nalu)
	f.Add(seed)

	// Seed: multi-NALU (two back-to-back).
	multi := append(seed, seed...)
	f.Add(multi)

	// Seed: empty input.
	f.Add([]byte{})

	// Seed: single byte (truncated length field).
	f.Add([]byte{0x00})

	// Seed: exactly 4 bytes but length=0 (zero-length NALU).
	f.Add([]byte{0x00, 0x00, 0x00, 0x00})

	// Seed: 4-byte length pointing past end of buffer.
	f.Add([]byte{0x00, 0x00, 0x00, 0xFF, 0x65})

	// Seed: very large length value in first 4 bytes.
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x65, 0x01})

	// Seed: length exactly matches remaining data.
	f.Add([]byte{0x00, 0x00, 0x00, 0x01, 0x65})

	// Seed: 3 bytes (incomplete length prefix).
	f.Add([]byte{0x00, 0x00, 0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic — any return value is acceptable.
		_ = AVC1ToAnnexB(data)
	})
}

func FuzzAnnexBToAVC1(f *testing.F) {
	// Seed: valid Annex B with 4-byte start code + IDR NALU.
	f.Add([]byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x01, 0x02, 0x03})

	// Seed: valid Annex B with 3-byte start code.
	f.Add([]byte{0x00, 0x00, 0x01, 0x65, 0x01, 0x02})

	// Seed: two NALUs with 4-byte start codes.
	f.Add([]byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0xC0, 0x1E, // SPS
		0x00, 0x00, 0x00, 0x01, 0x68, 0xCE, 0x38, 0x80, // PPS
	})

	// Seed: mixed 3-byte and 4-byte start codes.
	f.Add([]byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, // 4-byte start code
		0x00, 0x00, 0x01, 0x68, 0xCE, // 3-byte start code
	})

	// Seed: empty input.
	f.Add([]byte{})

	// Seed: no start codes — just random data.
	f.Add([]byte{0x01, 0x02, 0x03, 0x04})

	// Seed: start code at very end with no NALU body.
	f.Add([]byte{0x00, 0x00, 0x00, 0x01})

	// Seed: 3-byte start code at end with no NALU body.
	f.Add([]byte{0x00, 0x00, 0x01})

	// Seed: overlapping zero bytes that look like start codes.
	f.Add([]byte{0x00, 0x00, 0x00, 0x00, 0x01, 0x65})

	// Seed: data between start codes that is empty.
	f.Add([]byte{0x00, 0x00, 0x01, 0x00, 0x00, 0x01, 0x65})

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = AnnexBToAVC1(data)
	})
}

func FuzzExtractNALUs(f *testing.F) {
	// Seed: valid single NALU.
	nalu := []byte{0x65, 0x01, 0x02, 0x03}
	seed := make([]byte, 4+len(nalu))
	binary.BigEndian.PutUint32(seed[:4], uint32(len(nalu)))
	copy(seed[4:], nalu)
	f.Add(seed)

	// Seed: two NALUs back-to-back.
	multi := append(seed, seed...)
	f.Add(multi)

	// Seed: empty.
	f.Add([]byte{})

	// Seed: length=0.
	f.Add([]byte{0x00, 0x00, 0x00, 0x00})

	// Seed: length > remaining data.
	f.Add([]byte{0x00, 0x00, 0x01, 0x00, 0x65})

	// Seed: length exactly 1, single-byte NALU.
	f.Add([]byte{0x00, 0x00, 0x00, 0x01, 0x65})

	// Seed: very large length value.
	f.Add([]byte{0x7F, 0xFF, 0xFF, 0xFF, 0x65})

	// Seed: three bytes (incomplete length prefix).
	f.Add([]byte{0x00, 0x00, 0x00})

	// Seed: multiple NALUs of varying sizes.
	var multiVar []byte
	for _, n := range [][]byte{{0x67, 0x42}, {0x68, 0xCE, 0x38}, {0x65, 0x01, 0x02, 0x03}} {
		buf := make([]byte, 4+len(n))
		binary.BigEndian.PutUint32(buf[:4], uint32(len(n)))
		copy(buf[4:], n)
		multiVar = append(multiVar, buf...)
	}
	f.Add(multiVar)

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = ExtractNALUs(data)
	})
}

// ---------- ADTS fuzz targets ----------

func FuzzBuildADTS(f *testing.F) {
	// Seed: standard AAC-LC parameters.
	f.Add(48000, 2, 1024)
	f.Add(44100, 2, 1024)
	f.Add(48000, 1, 1024)

	// Seed: edge-case sample rates.
	f.Add(96000, 2, 1024)
	f.Add(7350, 1, 512)

	// Seed: unrecognized sample rate (escape value).
	f.Add(12345, 2, 1024)

	// Seed: zero values.
	f.Add(0, 0, 0)

	// Seed: negative values.
	f.Add(-1, -1, -1)
	f.Add(48000, 2, -100)

	// Seed: very large values.
	f.Add(1<<30, 255, 1<<20)
	f.Add(48000, 2, 1<<24)

	// Seed: channel edge cases.
	f.Add(48000, 0, 1024)
	f.Add(48000, 8, 1024)
	f.Add(48000, 7, 1024)

	f.Fuzz(func(t *testing.T, sampleRate, channels, frameLen int) {
		// Must not panic.
		_ = BuildADTS(sampleRate, channels, frameLen)
	})
}

func FuzzEnsureADTS(f *testing.F) {
	// Seed: raw AAC frame bytes (no ADTS header).
	f.Add([]byte{0x01, 0x02, 0x03, 0x04, 0x05}, 48000, 2)

	// Seed: data that already has an ADTS header.
	header := BuildADTS(48000, 2, 4)
	withHeader := append(header, 0x01, 0x02, 0x03, 0x04)
	f.Add(withHeader, 48000, 2)

	// Seed: empty data.
	f.Add([]byte{}, 48000, 2)

	// Seed: single byte.
	f.Add([]byte{0xFF}, 48000, 2)

	// Seed: data starting with 0xFF but not valid ADTS (wrong second byte).
	f.Add([]byte{0xFF, 0x00, 0x01, 0x02}, 48000, 2)

	// Seed: partial ADTS sync word.
	f.Add([]byte{0xFF, 0xF1, 0x00}, 48000, 2)

	// Seed: unusual sample rate and channels.
	f.Add([]byte{0x01, 0x02}, 0, 0)
	f.Add([]byte{0x01, 0x02}, -1, 255)

	f.Fuzz(func(t *testing.T, data []byte, sampleRate, channels int) {
		// Must not panic.
		_ = EnsureADTS(data, sampleRate, channels)
	})
}

func FuzzSplitADTSFrames(f *testing.F) {
	// Seed: single valid ADTS frame.
	payload := []byte{0x01, 0x02, 0x03, 0x04}
	header := BuildADTS(48000, 2, len(payload))
	frame := append(header, payload...)
	f.Add(frame)

	// Seed: two concatenated ADTS frames.
	twoFrames := append(frame, frame...)
	f.Add(twoFrames)

	// Seed: empty data.
	f.Add([]byte{})

	// Seed: non-ADTS data (returned as single raw payload).
	f.Add([]byte{0x01, 0x02, 0x03})

	// Seed: ADTS sync word but truncated header (< 7 bytes).
	f.Add([]byte{0xFF, 0xF1, 0x00, 0x00})

	// Seed: valid ADTS header but frame length = 0 (should be caught as < 7).
	corruptedZeroLen := make([]byte, 7)
	copy(corruptedZeroLen, header)
	// Zero out frame length bits in bytes 3-5.
	corruptedZeroLen[3] &= 0xFC // clear low 2 bits
	corruptedZeroLen[4] = 0x00
	corruptedZeroLen[5] &= 0x1F // clear high 3 bits
	f.Add(corruptedZeroLen)

	// Seed: ADTS header with frame length pointing past end of data.
	f.Add(header) // header says frameLen = 7 + 4 = 11, but data is only 7 bytes

	// Seed: corrupted sync word (0xFF but wrong second byte).
	f.Add([]byte{0xFF, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05})

	// Seed: three frames, last truncated mid-frame.
	threeFrames := append(twoFrames, frame[:5]...)
	f.Add(threeFrames)

	// Regression: CRC-present ADTS (protection_absent=0) with frame length < header length.
	// This caused makeslice panic when frameLen(7) - hdrLen(9) went negative.
	f.Add([]byte("\xff\xfa00\x01\x0100"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic.
		_ = SplitADTSFrames(data)
	})
}
