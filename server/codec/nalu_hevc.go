package codec

// HEVC NAL unit type constants as defined in ITU-T H.265 Table 7-1.
const (
	HEVCNALTrailN     = 0
	HEVCNALTrailR     = 1
	HEVCNALBlaWLP     = 16
	HEVCNALBlaWRadl   = 17
	HEVCNALBlaNLP     = 18
	HEVCNALIDRWRadl   = 19
	HEVCNALIDRNLP     = 20
	HEVCNALCraNut     = 21
	HEVCNALVPS        = 32
	HEVCNALSPS        = 33
	HEVCNALPPS        = 34
	HEVCNALAUD        = 35
	HEVCNALFillerData = 38
	HEVCNALSEIPrefix  = 39
	HEVCNALSEISuffix  = 40
)

// HEVCNALUType extracts the NAL unit type from the first byte of an HEVC NALU.
// HEVC NAL header is 2 bytes: forbidden(1) | type(6) | layerID(6) | tid(3).
// The type field occupies bits 1-6 of the first byte.
func HEVCNALUType(firstByte byte) byte {
	return (firstByte >> 1) & 0x3F
}

// IsHEVCKeyframe returns true if the NAL type represents an HEVC random access
// point (BLA types 16-18, IDR types 19-20, or CRA type 21).
func IsHEVCKeyframe(nalType byte) bool {
	return nalType >= HEVCNALBlaWLP && nalType <= HEVCNALCraNut
}

// IsHEVCVCL returns true if the NAL type is a Video Coding Layer unit (types 0-31).
func IsHEVCVCL(nalType byte) bool {
	return nalType <= 31
}

// HVC1ToAnnexB converts HVC1 format (4-byte length-prefixed NALUs) to
// Annex B format (start-code-prefixed NALUs). The wire format is identical
// to AVC1, so this delegates to the existing implementation.
// Returns nil for nil or empty input.
func HVC1ToAnnexB(hvc1 []byte) []byte {
	return AVC1ToAnnexB(hvc1)
}

// HVC1ToAnnexBInto converts HVC1 format to Annex B format, writing into dst.
// If dst has insufficient capacity, a new buffer is allocated. Returns the
// result slice (possibly resliced dst). Pass dst[:0] to reuse a buffer.
func HVC1ToAnnexBInto(hvc1 []byte, dst []byte) []byte {
	return AVC1ToAnnexBInto(hvc1, dst)
}

// AnnexBToHVC1 converts Annex B format (start-code-prefixed NALUs) to
// HVC1 format (4-byte length-prefixed NALUs). The wire format is identical
// to AVC1, so this delegates to the existing implementation.
// Returns nil for nil or empty input.
func AnnexBToHVC1(annexB []byte) []byte {
	return AnnexBToAVC1(annexB)
}

// AnnexBToHVC1Into converts Annex B format to HVC1 format, appending the
// result to dst. Returns the (possibly grown) dst slice.
// Pass dst[:0] to reuse a buffer without allocating.
func AnnexBToHVC1Into(annexB []byte, dst []byte) []byte {
	return AnnexBToAVC1Into(annexB, dst)
}

// PrependVPSSPSPPS prepends VPS, SPS, and PPS NALUs with Annex B start codes
// to the given Annex B data. Safely handles nil/empty individual parameters.
func PrependVPSSPSPPS(vps, sps, pps, annexBData []byte) []byte {
	if len(vps) == 0 && len(sps) == 0 && len(pps) == 0 {
		return annexBData
	}
	var buf []byte
	if len(vps) > 0 {
		buf = append(buf, annexBStartCode...)
		buf = append(buf, vps...)
	}
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

// PrependVPSSPSPPSInto prepends VPS, SPS, and PPS NALUs to annexBData, writing
// into dst. Returns the result slice. Pass dst[:0] to reuse a buffer.
func PrependVPSSPSPPSInto(vps, sps, pps, annexBData []byte, dst []byte) []byte {
	if len(vps) == 0 && len(sps) == 0 && len(pps) == 0 {
		if cap(dst) < len(annexBData) {
			return append(dst[:0], annexBData...)
		}
		dst = dst[:len(annexBData)]
		copy(dst, annexBData)
		return dst
	}

	needed := len(annexBData)
	if len(vps) > 0 {
		needed += 4 + len(vps)
	}
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
	if len(vps) > 0 {
		dst = append(dst, annexBStartCode...)
		dst = append(dst, vps...)
	}
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

// ExtractHEVCParamSets scans Annex B data for VPS (type 32), SPS (type 33),
// and PPS (type 34) NALUs. Returns copies of the first VPS, SPS, and PPS
// found, or nil for any that are not present. The returned slices are
// independent copies (safe to store).
func ExtractHEVCParamSets(annexB []byte) (vps, sps, pps []byte) {
	if len(annexB) == 0 {
		return nil, nil, nil
	}

	nalus := splitAnnexBNALUs(annexB)
	for _, nalu := range nalus {
		if len(nalu) < 2 {
			continue
		}
		nalType := HEVCNALUType(nalu[0])
		switch nalType {
		case HEVCNALVPS:
			if vps == nil {
				vps = make([]byte, len(nalu))
				copy(vps, nalu)
			}
		case HEVCNALSPS:
			if sps == nil {
				sps = make([]byte, len(nalu))
				copy(sps, nalu)
			}
		case HEVCNALPPS:
			if pps == nil {
				pps = make([]byte, len(nalu))
				copy(pps, nalu)
			}
		}
	}
	return vps, sps, pps
}

// WireToAnnexB converts wire format (4-byte length-prefixed NALUs) to Annex B
// (start-code-prefixed NALUs). Works for both AVC and HEVC since the wire
// format is identical.
func WireToAnnexB(data []byte) []byte {
	return AVC1ToAnnexB(data)
}

// WireToAnnexBInto is the in-place version of WireToAnnexB.
func WireToAnnexBInto(data []byte, dst []byte) []byte {
	return AVC1ToAnnexBInto(data, dst)
}

// AnnexBToWire converts Annex B (start-code-prefixed NALUs) to wire format
// (4-byte length-prefixed NALUs). Works for both AVC and HEVC.
func AnnexBToWire(annexB []byte) []byte {
	return AnnexBToAVC1(annexB)
}

// AnnexBToWireInto is the in-place version of AnnexBToWire.
func AnnexBToWireInto(annexB []byte, dst []byte) []byte {
	return AnnexBToAVC1Into(annexB, dst)
}
