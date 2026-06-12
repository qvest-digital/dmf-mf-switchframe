package caption

import "testing"

func TestFromS12MBytes(t *testing.T) {
	tests := []struct {
		name  string
		bytes []byte
		want  Timecode
		ok    bool
	}{
		{
			// Golden fixture from feedmonitor/file.ts (HEVC 1080p 29.97): the
			// first decoded frame's AV_FRAME_DATA_S12M_TIMECODE side data, which
			// ffprobe 8.0.1 reports as timecode 11:48:54:16. Little-endian:
			// count=1, packed word 0x16544811 -> BCD bytes 0x11 0x48 0x54 0x16.
			name:  "golden file.ts first frame 11:48:54:16",
			bytes: []byte{0x01, 0x00, 0x00, 0x00, 0x11, 0x48, 0x54, 0x16},
			want:  Timecode{Hours: 11, Minutes: 48, Seconds: 54, Frames: 16},
			ok:    true,
		},
		{
			name:  "midnight non-drop 00:00:00:00",
			bytes: []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			want:  Timecode{},
			ok:    true,
		},
		{
			// Bit 30 set -> drop-frame; fields otherwise zero.
			name:  "drop-frame flag",
			bytes: []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x40},
			want:  Timecode{DropFrame: true},
			ok:    true,
		},
		{
			name:  "count zero is not ok",
			bytes: []byte{0x00, 0x00, 0x00, 0x00, 0x11, 0x48, 0x54, 0x16},
			ok:    false,
		},
		{name: "too short is not ok", bytes: []byte{0x01, 0x00, 0x00}, ok: false},
		{name: "nil is not ok", bytes: nil, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FromS12MBytes(tt.bytes)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
