package caption

import "encoding/binary"

// ATSC A/53 constants for CEA-608/708 in H.264 SEI NALUs.
const (
	// ITU-T T.35 country code for United States.
	seiCountryCode = 0xB5

	// ATSC provider code.
	seiProviderCode = 0x0031

	// "GA94" user identifier (ATSC A/53).
	seiUserID0 = 'G'
	seiUserID1 = 'A'
	seiUserID2 = '9'
	seiUserID3 = '4'

	// User data type code for cc_data (CEA-708).
	seiCCDataType = 0x03

	// NALU type for SEI.
	naluTypeSEI = 6

	// SEI payload type for user_data_registered_itu_t_t35.
	seiPayloadTypeT35 = 4

	// Annex B start code.
	annexBSC0 = 0x00
	annexBSC1 = 0x00
	annexBSC2 = 0x00
	annexBSC3 = 0x01
)

// BuildSEINALU constructs an H.264 SEI NALU containing CEA-608/708 caption data
// in ATSC A/53 format (user_data_registered_itu_t_t35).
//
// The NALU structure is:
//
//	[start_code 00 00 00 01] [nalu_header 06] [sei_payload] [rbsp_trailing 80]
//
// The SEI payload contains:
//
//	[payload_type 04] [payload_size] [country_code B5] [provider_code 00 31]
//	[user_id "GA94"] [user_data_type_code 03]
//	[cc_data: process|count] [cc_valid|type|data0|data1]... [marker FF]
//
// Emulation prevention bytes (0x03) are inserted where needed.
func BuildSEINALU(pairs []CCPair) []byte {
	if len(pairs) == 0 {
		return nil
	}

	// cc_count is a 5-bit field (max 31). Each input pair produces 2 triples
	// (field 1 + field 2 null), so max input is 15 pairs → 30 triples.
	if len(pairs) > 15 {
		pairs = pairs[:15]
	}

	// Build the raw SEI payload (before EPB insertion).
	// Each field 1 pair gets a field 2 null triple for decoder compatibility.
	// cc_data_pkt triple: [cc_valid(1)|cc_type(2bits)] [cc_data_1] [cc_data_2] = 3 bytes
	ccCount := len(pairs) * 2 // field 1 + field 2 null per pair
	// payload = country_code(1) + provider_code(2) + user_id(4) + user_data_type(1) +
	//           process_em_data|cc_count(1) + em_data(1) + cc_data(3*N) + marker(1)
	payloadSize := 1 + 2 + 4 + 1 + 1 + 1 + 3*ccCount + 1

	// Allocate generous buffer: start_code(4) + nalu_header(1) + payload_type(1) +
	// payload_size(variable) + payload + trailing(1) + room for EPB
	buf := make([]byte, 0, 4+1+1+2+payloadSize+1+payloadSize/2)

	// Annex B start code.
	buf = append(buf, annexBSC0, annexBSC1, annexBSC2, annexBSC3)

	// NALU header: forbidden_zero_bit(0) | nal_ref_idc(00) | nal_unit_type(6=SEI)
	buf = append(buf, naluTypeSEI)

	// SEI message: payload_type = 4 (user_data_registered_itu_t_t35)
	buf = append(buf, seiPayloadTypeT35)

	// payload_size — may need multi-byte encoding if >= 255, but caption payloads
	// are always small (< 100 bytes for typical caption data).
	if payloadSize < 255 {
		buf = append(buf, byte(payloadSize))
	} else {
		// Multi-byte size encoding: 0xFF bytes followed by remainder.
		for payloadSize >= 255 {
			buf = append(buf, 0xFF)
			payloadSize -= 255
		}
		buf = append(buf, byte(payloadSize))
	}

	// Payload start — mark position for EPB scanning.
	payloadStart := len(buf)

	// country_code = 0xB5 (United States)
	buf = append(buf, seiCountryCode)

	// provider_code = 0x0031 (ATSC)
	buf = append(buf, byte(seiProviderCode>>8), byte(seiProviderCode))

	// user_identifier = "GA94"
	buf = append(buf, seiUserID0, seiUserID1, seiUserID2, seiUserID3)

	// user_data_type_code = 0x03 (cc_data)
	buf = append(buf, seiCCDataType)

	// process_em_data_flag(1) | process_cc_data_flag(1) | additional_data_flag(0) |
	// cc_count(5 bits)
	// Bit layout: 1_1_0_CCCCC
	ccCountByte := byte(0xC0) | byte(ccCount&0x1F)
	buf = append(buf, ccCountByte)

	// em_data byte (required by ATSC A/53, must be 0xFF).
	buf = append(buf, 0xFF)

	// cc_data_pkt triples: field 1 pair + field 2 null for each input pair.
	for _, pair := range pairs {
		// Field 1: cc_valid(1) | cc_type=0b00 (CEA-608 field 1)
		// Marker bits(11111) | cc_valid(1) | cc_type(00) = 0xFC
		buf = append(buf, 0xFC, pair.Data[0], pair.Data[1])
		// Field 2 null: cc_valid(0) | cc_type=0b01 (CEA-608 field 2) = 0xF9
		// cc_valid=0 tells decoders there is no field 2 data (avoids false CC2 activation).
		buf = append(buf, 0xF9, 0x80, 0x80)
	}

	// Marker byte 0xFF.
	buf = append(buf, 0xFF)

	// RBSP trailing bits: 0x80
	buf = append(buf, 0x80)

	// Insert emulation prevention bytes (EPB) in the NALU body (after start code).
	// Scan from the NALU header onward for 0x000000, 0x000001, 0x000002, 0x000003
	// patterns and insert 0x03 before the offending byte.
	buf = insertEPB(buf, payloadStart)

	return buf
}

// insertEPB inserts emulation prevention bytes into the NALU body.
// Scans from startOffset to end of buf for 00 00 {00,01,02,03} patterns
// and inserts 0x03 before the third byte.
func insertEPB(buf []byte, startOffset int) []byte {
	// We need to scan the NALU body (after start code) for EPB.
	// The start code is bytes 0-3, NALU header is byte 4.
	// EPB scanning starts from byte 5 (the SEI payload type).
	scanStart := 5
	if startOffset > scanStart {
		scanStart = startOffset
	}

	// Count needed insertions first to allocate once.
	insertions := 0
	for i := scanStart; i < len(buf)-2; i++ {
		if buf[i] == 0x00 && buf[i+1] == 0x00 && buf[i+2] <= 0x03 {
			insertions++
			i += 2 // skip past the pattern
		}
	}

	if insertions == 0 {
		return buf
	}

	// Rebuild with EPB bytes inserted.
	result := make([]byte, 0, len(buf)+insertions)
	result = append(result, buf[:scanStart]...)

	for i := scanStart; i < len(buf); i++ {
		if i < len(buf)-2 && buf[i] == 0x00 && buf[i+1] == 0x00 && buf[i+2] <= 0x03 {
			result = append(result, 0x00, 0x00, 0x03, buf[i+2])
			i += 2
		} else {
			result = append(result, buf[i])
		}
	}

	return result
}

// InsertSEIBeforeVCLInto inserts an SEI NALU before the first VCL (Video Coding Layer)
// NALU in an Annex B bitstream. VCL NALUs have type 1 (non-IDR slice) or 5 (IDR slice).
//
// If dst has insufficient capacity, a new buffer is allocated.
// Returns the result slice. Pass dst[:0] to reuse a buffer.
func InsertSEIBeforeVCLInto(sei, annexB []byte, dst []byte) []byte {
	if len(sei) == 0 {
		// No SEI to insert — copy annexB as-is.
		needed := len(annexB)
		if cap(dst) < needed {
			dst = make([]byte, needed)
		} else {
			dst = dst[:needed]
		}
		copy(dst, annexB)
		return dst
	}

	// Find the first VCL NALU start code position.
	vclPos := findFirstVCL(annexB)

	if vclPos < 0 {
		// No VCL found — prepend SEI before everything.
		vclPos = 0
	}

	needed := vclPos + len(sei) + (len(annexB) - vclPos)
	if cap(dst) < needed {
		dst = make([]byte, needed)
	} else {
		dst = dst[:needed]
	}

	// Copy: [pre-VCL NALUs] [SEI] [VCL + rest]
	n := copy(dst, annexB[:vclPos])
	n += copy(dst[n:], sei)
	copy(dst[n:], annexB[vclPos:])

	return dst
}

// findFirstVCL returns the byte offset of the first VCL NALU's start code
// in an Annex B bitstream. Returns -1 if no VCL found.
func findFirstVCL(data []byte) int {
	i := 0
	for i+4 <= len(data) {
		// Look for start code.
		scLen := 0
		scPos := i
		if data[i] == 0x00 && data[i+1] == 0x00 {
			if i+3 < len(data) && data[i+2] == 0x00 && data[i+3] == 0x01 {
				scLen = 4
			} else if data[i+2] == 0x01 {
				scLen = 3
			}
		}

		if scLen > 0 {
			naluType := data[i+scLen] & 0x1F
			// VCL NALU types: 1 (non-IDR), 5 (IDR)
			if naluType == 1 || naluType == 5 {
				return scPos
			}
			i += scLen + 1
		} else {
			i++
		}
	}
	return -1
}

// ExtractCCPairsFromSEI extracts CCPair data from an ATSC A/53 SEI NALU payload.
// The input should be the raw NALU body (after start code and NALU header),
// starting at the SEI payload type byte. Returns nil if not a caption SEI.
//
// IMPORTANT: Callers must strip emulation prevention bytes (EPB) from the NALU
// body before calling this function. Wire-format H.264 NALUs contain 0x03 bytes
// inserted after 0x00 0x00 sequences; these must be removed first or parsing
// will produce incorrect results.
func ExtractCCPairsFromSEI(naluBody []byte) []CCPair {
	if len(naluBody) < 2 {
		return nil
	}

	// Parse SEI payload type.
	pos := 0
	payloadType := 0
	for pos < len(naluBody) && naluBody[pos] == 0xFF {
		payloadType += 255
		pos++
	}
	if pos >= len(naluBody) {
		return nil
	}
	payloadType += int(naluBody[pos])
	pos++

	if payloadType != seiPayloadTypeT35 {
		return nil
	}

	// Parse payload size.
	payloadSize := 0
	for pos < len(naluBody) && naluBody[pos] == 0xFF {
		payloadSize += 255
		pos++
	}
	if pos >= len(naluBody) {
		return nil
	}
	payloadSize += int(naluBody[pos])
	pos++

	if pos+payloadSize > len(naluBody) {
		return nil
	}

	payload := naluBody[pos : pos+payloadSize]

	// Verify ATSC A/53 header.
	if len(payload) < 9 {
		return nil
	}
	if payload[0] != seiCountryCode {
		return nil
	}
	providerCode := binary.BigEndian.Uint16(payload[1:3])
	if providerCode != seiProviderCode {
		return nil
	}
	if payload[3] != seiUserID0 || payload[4] != seiUserID1 ||
		payload[5] != seiUserID2 || payload[6] != seiUserID3 {
		return nil
	}
	if payload[7] != seiCCDataType {
		return nil
	}

	// Parse cc_count.
	ccCountByte := payload[8]
	ccCount := int(ccCountByte & 0x1F)

	// Skip em_data byte at offset 9 (required by ATSC A/53).
	// cc_data triples start at offset 10.
	if len(payload) < 10+3*ccCount {
		return nil
	}

	pairs := make([]CCPair, 0, ccCount)
	for i := 0; i < ccCount; i++ {
		offset := 10 + i*3
		ccValid := payload[offset]&0x04 != 0
		ccType := payload[offset] & 0x03
		// Only extract field 1 (cc_type=0x00) valid pairs, skip field 2 nulls.
		if ccValid && ccType == 0x00 {
			pairs = append(pairs, CCPair{Data: [2]byte{payload[offset+1], payload[offset+2]}})
		}
	}

	return pairs
}
