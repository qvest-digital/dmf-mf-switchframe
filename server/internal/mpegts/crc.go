package mpegts

// CRC32MPEG2 computes the CRC-32/MPEG-2 checksum used by MPEG-TS PSI tables
// and SCTE-35 sections. Polynomial: 0x04C11DB7, init: 0xFFFFFFFF, no final XOR.
func CRC32MPEG2(data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
