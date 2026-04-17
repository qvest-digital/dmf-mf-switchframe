package output

import (
	"testing"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal/mpegts"
)

func TestTSMuxer_SetSCTE35PID_ResetsContinuityCounter(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35PID(mpegts.SCTE35PID)
	m.SetOutput(func(data []byte) {})
	writeInitKeyframe(t, m)

	data := encodeSpliceNull(t)

	for i := 0; i < 30; i++ {
		if err := m.WriteSCTE35(data); err != nil {
			t.Fatalf("WriteSCTE35 iter %d: %v", i, err)
		}
		idr := &media.VideoFrame{
			PTS: int64(96000 + (i+1)*3000), DTS: int64(96000 + (i+1)*3000), IsKeyframe: true,
			SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
			PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
			WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
			Codec:    "h264",
		}
		if err := m.WriteVideo(idr); err != nil {
			t.Fatalf("WriteVideo iter %d: %v", i, err)
		}
	}

	m.SetSCTE35PID(mpegts.SCTE35PID)

	var output []byte
	m.SetOutput(func(d []byte) {
		output = append(output, d...)
	})

	if err := m.WriteSCTE35(data); err != nil {
		t.Fatalf("WriteSCTE35 post-reset: %v", err)
	}
	nextIDR := &media.VideoFrame{
		PTS: int64(96000 + 31*3000), DTS: int64(96000 + 31*3000), IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	if err := m.WriteVideo(nextIDR); err != nil {
		t.Fatalf("WriteVideo post-reset: %v", err)
	}

	cc, ok := firstCCForPID(output, mpegts.SCTE35PID)
	if !ok {
		t.Fatal("no SCTE-35 packet in output after SetSCTE35PID reset")
	}
	if cc != 0 {
		t.Fatalf("expected CC=0 after SetSCTE35PID reset, got CC=%d (bug: counter persisted across PID reinit)", cc)
	}
}

func TestTSMuxer_CloseReinit_ResetsSCTE35CC(t *testing.T) {
	m := NewTSMuxer()
	m.SetSCTE35PID(mpegts.SCTE35PID)
	m.SetOutput(func(data []byte) {})
	writeInitKeyframe(t, m)

	data := encodeSpliceNull(t)
	for i := 0; i < 30; i++ {
		_ = m.WriteSCTE35(data)
		idr := &media.VideoFrame{
			PTS: int64(96000 + (i+1)*3000), DTS: int64(96000 + (i+1)*3000), IsKeyframe: true,
			SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
			PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
			WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
			Codec:    "h264",
		}
		_ = m.WriteVideo(idr)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	m.SetSCTE35PID(mpegts.SCTE35PID)

	var output []byte
	m.SetOutput(func(d []byte) {
		output = append(output, d...)
	})
	writeInitKeyframe(t, m)

	if err := m.WriteSCTE35(data); err != nil {
		t.Fatalf("WriteSCTE35 post-reinit: %v", err)
	}
	idr := &media.VideoFrame{
		PTS: 99000, DTS: 99000, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
		PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
		Codec:    "h264",
	}
	if err := m.WriteVideo(idr); err != nil {
		t.Fatalf("WriteVideo post-reinit: %v", err)
	}

	cc, ok := firstCCForPID(output, mpegts.SCTE35PID)
	if !ok {
		t.Fatal("no SCTE-35 packet in output after Close+reinit")
	}
	if cc != 0 {
		t.Fatalf("expected CC=0 after Close+reinit, got CC=%d (bug: scte35CC persisted)", cc)
	}
}

func TestTSMuxer_CloseReinit_ResetsSDTNITCC(t *testing.T) {
	m := NewTSMuxer()
	m.SetOutput(func(data []byte) {})
	writeInitKeyframe(t, m)

	for i := 1; i <= 20; i++ {
		idr := &media.VideoFrame{
			PTS: int64(90000 + i*300000), DTS: int64(90000 + i*300000), IsKeyframe: true,
			SPS:      []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88},
			PPS:      []byte{0x68, 0xCE, 0x38, 0x80},
			WireData: []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x80, 0x40, 0x00},
			Codec:    "h264",
		}
		_ = m.WriteVideo(idr)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var output []byte
	m.SetOutput(func(d []byte) {
		output = append(output, d...)
	})
	writeInitKeyframe(t, m)

	sdtCC, sdtOK := firstCCForPID(output, mpegts.SDTPID)
	if !sdtOK {
		t.Fatal("no SDT packet in output after reinit")
	}
	if sdtCC != 0 {
		t.Errorf("expected SDT CC=0 after Close+reinit, got CC=%d (bug: sdtCC persisted)", sdtCC)
	}

	nitCC, nitOK := firstCCForPID(output, mpegts.NITPID)
	if !nitOK {
		t.Fatal("no NIT packet in output after reinit")
	}
	if nitCC != 0 {
		t.Errorf("expected NIT CC=0 after Close+reinit, got CC=%d (bug: nitCC persisted)", nitCC)
	}
}

func firstCCForPID(buf []byte, pid uint16) (uint8, bool) {
	for i := 0; i+mpegts.PacketSize <= len(buf); i += mpegts.PacketSize {
		pkt := buf[i : i+mpegts.PacketSize]
		if pkt[0] != 0x47 {
			continue
		}
		got := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if got == pid {
			return pkt[3] & 0x0F, true
		}
	}
	return 0, false
}
