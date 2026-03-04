package codec

// IsADTS reports whether data begins with an ADTS sync word (0xFFF in
// the top 12 bits of the first two bytes). Returns false for nil or
// data shorter than 2 bytes.
func IsADTS(data []byte) bool {
	if len(data) < 2 {
		return false
	}
	// ADTS sync word: 12 bits all set (0xFFF).
	return data[0] == 0xFF && (data[1]&0xF0) == 0xF0
}

// BuildADTS constructs a 7-byte ADTS header for an AAC-LC frame.
//
// The header assumes MPEG-4 (ID=0), AAC-LC profile (profile=1, stored
// as profile-1=0 in the 2-bit objectType field), no CRC (protection
// absent=1).
//
// Frame length in the header includes the 7-byte header itself.
//
// ADTS header layout (7 bytes, no CRC):
//
//	Byte 0:    sync word high 8 bits (0xFF)
//	Byte 1:    sync word low 4 bits (0xF) | ID (0=MPEG-4) | layer (00) | protection absent (1)
//	Byte 2:    profile (2 bits) | sample rate index (4 bits) | private (1 bit) | channel config high (1 bit)
//	Byte 3:    channel config low (2 bits) | originality (1) | home (1) | copyright ID (1) | copyright start (1) | frame length high (2 bits)
//	Byte 4:    frame length mid (8 bits)
//	Byte 5:    frame length low (3 bits) | buffer fullness high (5 bits)
//	Byte 6:    buffer fullness low (6 bits) | number of AAC frames - 1 (2 bits)
func BuildADTS(sampleRate, channels, frameLen int) []byte {
	header := make([]byte, 7)
	totalLen := frameLen + 7

	srIdx := sampleRateIndex(sampleRate)

	// Byte 0: sync word high byte.
	header[0] = 0xFF

	// Byte 1: sync word low nibble=0xF | ID=0 (MPEG-4) | layer=00 | protection_absent=1.
	header[1] = 0xF1

	// Byte 2: profile (2 bits) | sr index (4 bits) | private (1 bit) | channel config high (1 bit).
	// ADTS profile = audioObjectType - 1. AAC-LC = objectType 2, so profile = 1 (0b01).
	profile := byte(1)
	header[2] = (profile << 6) | (byte(srIdx) << 2) | (byte(channels>>2) & 0x01)

	// Byte 3: channel config low 2 bits | originality=0 | home=0 | copyright_id=0 | copyright_start=0 | frame length high 2 bits.
	header[3] = (byte(channels&0x03) << 6) | byte((totalLen>>11)&0x03)

	// Byte 4: frame length mid 8 bits.
	header[4] = byte((totalLen >> 3) & 0xFF)

	// Byte 5: frame length low 3 bits | buffer fullness high 5 bits (0x7FF = VBR).
	header[5] = byte((totalLen&0x07)<<5) | 0x1F

	// Byte 6: buffer fullness low 6 bits | number of AAC frames - 1 = 0.
	header[6] = 0xFC

	return header
}

// EnsureADTS returns data with an ADTS header prepended. If data
// already starts with an ADTS sync word, it is returned unchanged.
func EnsureADTS(data []byte, sampleRate, channels int) []byte {
	if IsADTS(data) {
		return data
	}
	header := BuildADTS(sampleRate, channels, len(data))
	out := make([]byte, 7+len(data))
	copy(out, header)
	copy(out[7:], data)
	return out
}

// sampleRateIndex maps a sample rate in Hz to the MPEG-4 Audio sample
// rate index (0-12). Returns 15 (escape value) for unrecognized rates.
func sampleRateIndex(rate int) int {
	switch rate {
	case 96000:
		return 0
	case 88200:
		return 1
	case 64000:
		return 2
	case 48000:
		return 3
	case 44100:
		return 4
	case 32000:
		return 5
	case 24000:
		return 6
	case 22050:
		return 7
	case 16000:
		return 8
	case 12000:
		return 9
	case 11025:
		return 10
	case 8000:
		return 11
	case 7350:
		return 12
	default:
		return 15
	}
}
