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

// AVC1ToAnnexBInto converts AVC1 format to Annex B format, writing into dst.
// If dst has insufficient capacity, a new buffer is allocated. Returns the
// result slice (possibly resliced dst). Pass dst[:0] to reuse a buffer.
func AVC1ToAnnexBInto(avc1 []byte, dst []byte) []byte {
	if len(avc1) == 0 {
		if dst != nil {
			return dst[:0]
		}
		return nil
	}

	if cap(dst) < len(avc1) {
		dst = make([]byte, len(avc1))
	}
	dst = dst[:len(avc1)]
	pos := 0

	for pos+4 <= len(avc1) {
		naluLen := int(binary.BigEndian.Uint32(avc1[pos : pos+4]))
		if naluLen <= 0 || pos+4+naluLen > len(avc1) {
			break
		}

		copy(dst[pos:pos+4], annexBStartCode)
		copy(dst[pos+4:pos+4+naluLen], avc1[pos+4:pos+4+naluLen])
		pos += 4 + naluLen
	}

	return dst[:pos]
}

// AnnexBToAVC1 converts Annex B format (start-code-prefixed NALUs) to
// AVC1 format (4-byte length-prefixed NALUs). Handles both 3-byte
// (0x000001) and 4-byte (0x00000001) start codes. Returns nil for nil
// or empty input.
func AnnexBToAVC1(annexB []byte) []byte {
	return AnnexBToAVC1Into(annexB, nil)
}

// AnnexBToAVC1Into converts Annex B format to AVC1 format, appending
// the result to dst. Returns the (possibly grown) dst slice.
// Pass dst[:0] to reuse a buffer without allocating.
// Returns nil for nil or empty input.
func AnnexBToAVC1Into(annexB []byte, dst []byte) []byte {
	if len(annexB) == 0 {
		return nil
	}

	nalus := splitAnnexBNALUs(annexB)
	if len(nalus) == 0 {
		return nil
	}

	totalLen := 0
	for _, nalu := range nalus {
		totalLen += 4 + len(nalu)
	}

	if cap(dst)-len(dst) < totalLen {
		grown := make([]byte, len(dst), len(dst)+totalLen)
		copy(grown, dst)
		dst = grown
	}

	pos := len(dst)
	dst = dst[:pos+totalLen]

	for _, nalu := range nalus {
		binary.BigEndian.PutUint32(dst[pos:pos+4], uint32(len(nalu)))
		copy(dst[pos+4:], nalu)
		pos += 4 + len(nalu)
	}

	return dst
}

// ExtractNALUs extracts individual NALUs from AVC1 format data,
// returning each NALU body without the 4-byte length prefix.
// The returned slices are sub-slices of the input — callers that
// need to own the data (e.g. SPS/PPS storage) must copy it themselves.
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

		nalus = append(nalus, avc1[pos+4:pos+4+naluLen])
		pos += 4 + naluLen
	}

	return nalus
}

// splitAnnexBNALUs splits an Annex B byte stream into individual NALUs.
// Handles both 3-byte (0x000001) and 4-byte (0x00000001) start codes.
// Returns sub-slices of the input data (zero-copy).
func splitAnnexBNALUs(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}

	var nalus [][]byte
	naluStart := -1 // byte offset where current NALU body begins

	i := 0
	for i < len(data)-2 {
		if data[i] == 0x00 && data[i+1] == 0x00 {
			scLen := 0
			if i+3 < len(data) && data[i+2] == 0x00 && data[i+3] == 0x01 {
				scLen = 4
			} else if data[i+2] == 0x01 {
				scLen = 3
			}
			if scLen > 0 {
				if naluStart >= 0 {
					nalu := data[naluStart:i]
					if len(nalu) > 0 {
						nalus = append(nalus, nalu)
					}
				}
				naluStart = i + scLen
				i += scLen
				continue
			}
		}
		i++
	}

	if naluStart >= 0 && naluStart < len(data) {
		nalu := data[naluStart:]
		if len(nalu) > 0 {
			nalus = append(nalus, nalu)
		}
	}

	return nalus
}

// PrependSPSPPS prepends SPS and PPS NALUs with Annex B start codes
// to the given Annex B data. Safely handles nil/empty SPS or PPS.
func PrependSPSPPS(sps, pps, annexBData []byte) []byte {
	if len(sps) == 0 && len(pps) == 0 {
		return annexBData
	}
	var buf []byte
	if len(sps) > 0 {
		buf = append(buf, annexBStartCode...)
		buf = append(buf, sps...)
	}
	if len(pps) > 0 {
		buf = append(buf, annexBStartCode...)
		buf = append(buf, pps...)
	}
	return append(buf, annexBData...)
}

// PrependSPSPPSInto prepends SPS and PPS NALUs to annexBData, writing into dst.
// Returns the result slice. Pass dst[:0] to reuse a buffer.
func PrependSPSPPSInto(sps, pps, annexBData []byte, dst []byte) []byte {
	if len(sps) == 0 && len(pps) == 0 {
		if cap(dst) < len(annexBData) {
			return append(dst[:0], annexBData...)
		}
		dst = dst[:len(annexBData)]
		copy(dst, annexBData)
		return dst
	}

	needed := len(annexBData)
	if len(sps) > 0 {
		needed += 4 + len(sps)
	}
	if len(pps) > 0 {
		needed += 4 + len(pps)
	}

	if cap(dst) < needed {
		dst = make([]byte, 0, needed)
	}
	dst = dst[:0]
	if len(sps) > 0 {
		dst = append(dst, annexBStartCode...)
		dst = append(dst, sps...)
	}
	if len(pps) > 0 {
		dst = append(dst, annexBStartCode...)
		dst = append(dst, pps...)
	}
	dst = append(dst, annexBData...)
	return dst
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
