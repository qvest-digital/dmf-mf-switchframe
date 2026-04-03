package mpegts

import "testing"

func TestPIDValues(t *testing.T) {
	tests := []struct {
		name string
		pid  uint16
		want uint16
	}{
		{"PAT", PATPID, 0x0000},
		{"NIT", NITPID, 0x0010},
		{"SDT", SDTPID, 0x0011},
		{"Video", VideoPID, 0x0100},
		{"Audio", AudioPID, 0x0101},
		{"SCTE-35", SCTE35PID, 0x0102},
		{"PMT", PMTPID, 0x1000},
		{"Null", NullPID, 0x1FFF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.pid != tt.want {
				t.Errorf("%s PID = 0x%04X, want 0x%04X", tt.name, tt.pid, tt.want)
			}
		})
	}
}

func TestTableIDValues(t *testing.T) {
	tests := []struct {
		name string
		id   uint8
		want uint8
	}{
		{"PAT", TableIDPAT, 0x00},
		{"PMT", TableIDPMT, 0x02},
		{"NIT", TableIDNIT, 0x40},
		{"SDT", TableIDSDT, 0x42},
		{"SCTE-35", TableIDSCTE35, 0xFC},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.id != tt.want {
				t.Errorf("TableID %s = 0x%02X, want 0x%02X", tt.name, tt.id, tt.want)
			}
		})
	}
}

func TestStreamTypeValues(t *testing.T) {
	tests := []struct {
		name       string
		streamType uint8
		want       uint8
	}{
		{"H.264", StreamTypeH264, 0x1B},
		{"HEVC", StreamTypeHEVC, 0x24},
		{"AAC", StreamTypeAAC, 0x0F},
		{"SCTE-35", StreamTypeSCTE35, 0x86},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.streamType != tt.want {
				t.Errorf("StreamType %s = 0x%02X, want 0x%02X", tt.name, tt.streamType, tt.want)
			}
		})
	}
}

func TestPIDsAreDistinct(t *testing.T) {
	pids := map[uint16]string{
		PATPID:    "PAT",
		NITPID:    "NIT",
		SDTPID:    "SDT",
		VideoPID:  "Video",
		AudioPID:  "Audio",
		SCTE35PID: "SCTE-35",
		PMTPID:    "PMT",
		NullPID:   "Null",
	}
	if len(pids) != 8 {
		t.Fatal("PID values are not all distinct")
	}
}
