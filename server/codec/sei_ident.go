package codec

import "fmt"

// SwitchframeUUID is the unique identifier for Switchframe encoder metadata
// in SEI user_data_unregistered messages (payload type 5). This is a v4 UUID
// hardcoded so every Switchframe installation uses the same value, making
// Switchframe-encoded streams identifiable by downstream tools.
//
// UUID: 8a4f932b-6d1e-4c7a-9f3d-5c8b2e7a1d4f
var SwitchframeUUID = [16]byte{
	0x8a, 0x4f, 0x93, 0x2b, 0x6d, 0x1e, 0x4c, 0x7a,
	0x9f, 0x3d, 0x5c, 0x8b, 0x2e, 0x7a, 0x1d, 0x4f,
}

// EncoderIdentInfo contains encoder parameters for the identification SEI.
type EncoderIdentInfo struct {
	EncoderName string // e.g., "hevc_nvenc", "libx265", "libx264"
	Profile     string // e.g., "main422-10", "high", "main"
	Width       int
	Height      int
	FPSNum      int
	FPSDen      int
	BitDepth    int    // 8 or 10
	Chroma      string // "420" or "422"
}

// BuildEncoderIdentSEI creates an SEI NALU containing Switchframe encoder
// identification metadata as a user_data_unregistered message (payload type 5).
// Injected on keyframes so downstream tools can identify the encoding source.
//
// The codec parameter selects the NALU framing:
//   - "h264" or "": H.264 SEI (1-byte header 0x06)
//   - "h265": HEVC PREFIX_SEI (2-byte header 0x4E 0x01)
//
// The NALU structure is:
//
//	[start_code 00 00 00 01] [nalu_header] [payload_type 05] [payload_size]
//	[uuid 16 bytes] [user_data UTF-8 string] [rbsp_trailing 80]
//
// The user data string format:
//
//	Switchframe/1.0 encoder=<name> profile=<profile> format=<height>p<fps>
func BuildEncoderIdentSEI(codec string, info EncoderIdentInfo) []byte {
	isHEVC := codec == "h265"

	// Build the user data string.
	fpsStr := formatFPS(info.FPSNum, info.FPSDen)
	userData := fmt.Sprintf("Switchframe/1.0 encoder=%s profile=%s format=%dp%s",
		info.EncoderName, info.Profile, info.Height, fpsStr)

	// payload_size = 16 (UUID) + len(user_data)
	payloadSize := 16 + len(userData)

	// NALU header size: 1 byte for H.264, 2 bytes for HEVC.
	naluHeaderSize := 1
	if isHEVC {
		naluHeaderSize = 2
	}

	// Allocate buffer: start_code(4) + nalu_header + payload_type(1) +
	// payload_size(variable, usually 1-2 bytes) + payload + trailing(1) + EPB headroom
	buf := make([]byte, 0, 4+naluHeaderSize+1+2+payloadSize+1+payloadSize/4)

	// Annex B start code.
	buf = append(buf, 0x00, 0x00, 0x00, 0x01)

	// NALU header (codec-specific).
	if isHEVC {
		// HEVC PREFIX_SEI: 2-byte header (nal_unit_type=39, layer_id=0, tid=1).
		buf = append(buf, 0x4E, 0x01)
	} else {
		// H.264: forbidden_zero_bit(0) | nal_ref_idc(00) | nal_unit_type(6=SEI)
		buf = append(buf, 0x06)
	}

	// SEI message: payload_type = 5 (user_data_unregistered).
	buf = append(buf, 0x05)

	// Payload size — variable-length encoding (0xFF chunks + remainder).
	remaining := payloadSize
	for remaining >= 255 {
		buf = append(buf, 0xFF)
		remaining -= 255
	}
	buf = append(buf, byte(remaining))

	// Mark payload start for EPB scanning.
	payloadStart := len(buf)

	// UUID (16 bytes).
	buf = append(buf, SwitchframeUUID[:]...)

	// User data string (UTF-8).
	buf = append(buf, []byte(userData)...)

	// RBSP trailing bits.
	buf = append(buf, 0x80)

	// Insert emulation prevention bytes (EPB) in the NALU body.
	buf = insertIdentEPB(buf, payloadStart)

	return buf
}

// formatFPS formats FPS as a human-readable string. Recognizes common
// NTSC fractional rates (N*1000/1001) and formats them as standard
// broadcast notation (29.97, 59.94, 23.98).
func formatFPS(num, den int) string {
	if den == 0 {
		den = 1
	}
	// Check for NTSC fractional rates (N*1000/1001).
	if den == 1001 && num%1000 == 0 {
		base := num / 1000
		switch base {
		case 30:
			return "29.97"
		case 60:
			return "59.94"
		case 24:
			return "23.98"
		default:
			// Generic fractional rate.
			fps := float64(num) / float64(den)
			return fmt.Sprintf("%.2f", fps)
		}
	}
	// Integer rate.
	if den == 1 {
		return fmt.Sprintf("%d", num)
	}
	// Generic fractional rate.
	fps := float64(num) / float64(den)
	return fmt.Sprintf("%.2f", fps)
}

// insertIdentEPB inserts emulation prevention bytes into the NALU body.
// Scans from startOffset to end of buf for 00 00 {00,01,02,03} patterns
// and inserts 0x03 before the third byte.
func insertIdentEPB(buf []byte, startOffset int) []byte {
	// Scan from just after the NALU header for EPB patterns.
	scanStart := 5 // after start_code(4) + at least 1 byte of header
	if startOffset > scanStart {
		scanStart = startOffset
	}

	// First pass: count insertions needed.
	insertions := 0
	zeros := 0
	for i := scanStart; i < len(buf); i++ {
		b := buf[i]
		if zeros >= 2 && b <= 0x03 {
			insertions++
			zeros = 0
		}
		if b == 0x00 {
			zeros++
		} else {
			zeros = 0
		}
	}

	if insertions == 0 {
		return buf
	}

	// Second pass: rebuild with EPB bytes inserted.
	result := make([]byte, 0, len(buf)+insertions)
	result = append(result, buf[:scanStart]...)

	zeros = 0
	for i := scanStart; i < len(buf); i++ {
		b := buf[i]
		if zeros >= 2 && b <= 0x03 {
			result = append(result, 0x03)
			zeros = 0
		}
		result = append(result, b)
		if b == 0x00 {
			zeros++
		} else {
			zeros = 0
		}
	}

	return result
}
