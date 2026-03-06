package codec

import (
	"encoding/binary"
	"fmt"
)

// annexBStartCode is the 4-byte Annex B start code prefix.
var annexBStartCode = []byte{0x00, 0x00, 0x00, 0x01}

// AVC1ToAnnexB converts AVC1 format (4-byte length-prefixed NALUs) to
// Annex B format (start-code-prefixed NALUs). Returns nil for nil or
// empty input.
func AVC1ToAnnexB(avc1 []byte) []byte {
	if len(avc1) == 0 {
		return nil
	}

	// Output is same length: 4-byte length prefix replaced by 4-byte start code.
	out := make([]byte, len(avc1))
	pos := 0

	for pos+4 <= len(avc1) {
		naluLen := int(binary.BigEndian.Uint32(avc1[pos : pos+4]))
		if naluLen <= 0 || pos+4+naluLen > len(avc1) {
			break
		}

		copy(out[pos:pos+4], annexBStartCode)
		copy(out[pos+4:pos+4+naluLen], avc1[pos+4:pos+4+naluLen])
		pos += 4 + naluLen
	}

	return out[:pos]
}

// AnnexBToAVC1 converts Annex B format (start-code-prefixed NALUs) to
// AVC1 format (4-byte length-prefixed NALUs). Handles both 3-byte
// (0x000001) and 4-byte (0x00000001) start codes. Returns nil for nil
// or empty input.
func AnnexBToAVC1(annexB []byte) []byte {
	if len(annexB) == 0 {
		return nil
	}

	nalus := splitAnnexBNALUs(annexB)
	if len(nalus) == 0 {
		return nil
	}

	// Calculate output size: 4-byte length prefix + NALU data for each.
	totalLen := 0
	for _, nalu := range nalus {
		totalLen += 4 + len(nalu)
	}

	out := make([]byte, totalLen)
	pos := 0

	for _, nalu := range nalus {
		binary.BigEndian.PutUint32(out[pos:pos+4], uint32(len(nalu)))
		copy(out[pos+4:], nalu)
		pos += 4 + len(nalu)
	}

	return out
}

// ExtractNALUs extracts individual NALUs from AVC1 format data,
// returning each NALU body without the 4-byte length prefix.
// Returns nil for nil or empty input.
func ExtractNALUs(avc1 []byte) [][]byte {
	if len(avc1) == 0 {
		return nil
	}

	var nalus [][]byte
	pos := 0

	for pos+4 <= len(avc1) {
		naluLen := int(binary.BigEndian.Uint32(avc1[pos : pos+4]))
		if naluLen <= 0 || pos+4+naluLen > len(avc1) {
			break
		}

		nalu := make([]byte, naluLen)
		copy(nalu, avc1[pos+4:pos+4+naluLen])
		nalus = append(nalus, nalu)
		pos += 4 + naluLen
	}

	return nalus
}

// splitAnnexBNALUs splits an Annex B byte stream into individual NALUs.
// Handles both 3-byte (0x000001) and 4-byte (0x00000001) start codes.
func splitAnnexBNALUs(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}

	// Find all start code positions.
	var starts []int
	i := 0
	for i < len(data) {
		if i+3 <= len(data) && data[i] == 0x00 && data[i+1] == 0x00 {
			if i+4 <= len(data) && data[i+2] == 0x00 && data[i+3] == 0x01 {
				// 4-byte start code
				starts = append(starts, i)
				i += 4
				continue
			}
			if data[i+2] == 0x01 {
				// 3-byte start code
				starts = append(starts, i)
				i += 3
				continue
			}
		}
		i++
	}

	if len(starts) == 0 {
		return nil
	}

	var nalus [][]byte
	for idx, start := range starts {
		// Determine where the NALU data begins (after start code).
		naluStart := start + 3
		if start+3 < len(data) && data[start+2] == 0x00 {
			naluStart = start + 4
		}

		// Determine where the NALU data ends.
		var naluEnd int
		if idx+1 < len(starts) {
			naluEnd = starts[idx+1]
		} else {
			naluEnd = len(data)
		}

		nalu := data[naluStart:naluEnd]
		if len(nalu) > 0 {
			nalus = append(nalus, nalu)
		}
	}

	return nalus
}

// ParseSPSCodecString returns a WebCodecs-compatible codec string from SPS NALU bytes.
// The SPS NALU format is: [nalu_type_byte] [profile_idc] [constraint_flags] [level_idc] ...
// Example: "avc1.640028" for High profile Level 4.0.
func ParseSPSCodecString(sps []byte) string {
	if len(sps) < 4 {
		return "avc1.42C01E" // fallback: Baseline Level 3.0
	}
	return fmt.Sprintf("avc1.%02X%02X%02X", sps[1], sps[2], sps[3])
}
