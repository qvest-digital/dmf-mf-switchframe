package mpegts

// Well-known MPEG-TS Packet Identifiers (PIDs).
const (
	PATPID uint16 = 0x0000 // Program Association Table
	NITPID uint16 = 0x0010 // Network Information Table
	SDTPID uint16 = 0x0011 // Service Description Table

	VideoPID  uint16 = 0x0100 // H.264/HEVC video (Switchframe default)
	AudioPID  uint16 = 0x0101 // AAC audio (Switchframe default)
	SCTE35PID uint16 = 0x0102 // SCTE-35 splice info (Switchframe default)
	PMTPID    uint16 = 0x1000 // Program Map Table (matches go-astits default)

	// NullPID is already defined in mpegts.go as 0x1FFF
)

// MPEG-2 PSI Table IDs (ISO 13818-1 Table 2-31).
const (
	TableIDPAT    uint8 = 0x00 // Program Association Table
	TableIDPMT    uint8 = 0x02 // Program Map Table
	TableIDNIT    uint8 = 0x40 // Network Information Table (actual)
	TableIDSDT    uint8 = 0x42 // Service Description Table (actual)
	TableIDSCTE35 uint8 = 0xFC // SCTE-35 splice_info_section
)

// MPEG-TS Stream Types (ISO 13818-1 Table 2-36 + SCTE extensions).
const (
	StreamTypeH264   uint8 = 0x1B // H.264/AVC video
	StreamTypeHEVC   uint8 = 0x24 // H.265/HEVC video
	StreamTypeAAC    uint8 = 0x0F // AAC audio (ADTS)
	StreamTypeSCTE35 uint8 = 0x86 // SCTE-35 splice information
)
