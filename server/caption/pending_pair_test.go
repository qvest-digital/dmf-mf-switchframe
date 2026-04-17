package caption

import (
	"bytes"
	"testing"
)

func TestManager_PendingPair_ReturnsPairInAuthorMode(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)
	m.IngestText("Hi")

	pair, ok := m.PendingPair()
	if !ok {
		t.Fatal("PendingPair() returned ok=false in author mode, want ok=true")
	}
	if pair.IsNull() {
		t.Errorf("PendingPair() returned null pair %v, want a non-null pair", pair)
	}
}

func TestManager_PendingPair_ReturnsNullPairWhenAuthorIdle(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	pair, ok := m.PendingPair()
	if !ok {
		t.Fatal("PendingPair() returned ok=false in author mode, want ok=true (continuous carriage)")
	}
	if !pair.IsNull() {
		t.Errorf("PendingPair() returned %v in idle author mode, want null pair (0x80,0x80)", pair)
	}
}

func TestManager_PendingPair_ReturnsNullPairWhenModeOff(t *testing.T) {
	m := NewManager()

	pair, ok := m.PendingPair()
	if !ok {
		t.Fatal("PendingPair() returned ok=false in off mode, want ok=true (continuous carriage from frame zero)")
	}
	if !pair.IsNull() {
		t.Errorf("PendingPair() returned %v in off mode, want null pair", pair)
	}
}

func TestManager_PendingCaptionSEI_ProducesValidSEI(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)
	m.IngestText("AB")

	sei := m.PendingCaptionSEI("h264")
	if len(sei) == 0 {
		t.Fatal("PendingCaptionSEI() returned nil/empty in author mode, want valid SEI bytes")
	}

	if !bytes.HasPrefix(sei, []byte{0x00, 0x00, 0x00, 0x01}) {
		t.Errorf("PendingCaptionSEI() missing Annex B start code, got prefix %x", sei[:4])
	}

	if sei[4] != naluTypeSEI {
		t.Errorf("PendingCaptionSEI() NALU header byte = %02X, want %02X (SEI)", sei[4], naluTypeSEI)
	}

	if sei[5] != seiPayloadTypeT35 {
		t.Errorf("PendingCaptionSEI() SEI payload type = %02X, want %02X (T.35)", sei[5], seiPayloadTypeT35)
	}

	if sei[len(sei)-1] != 0x80 {
		t.Errorf("PendingCaptionSEI() last byte = %02X, want 0x80 (RBSP trailing bits)", sei[len(sei)-1])
	}
}

func TestManager_PendingCaptionSEI_HEVCHeader(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)
	m.IngestText("XY")

	sei := m.PendingCaptionSEI("h265")
	if len(sei) == 0 {
		t.Fatal("PendingCaptionSEI(h265) returned empty SEI")
	}

	if sei[4] != hevcSEIByte0 || sei[5] != hevcSEIByte1 {
		t.Errorf("PendingCaptionSEI(h265) header = %02X %02X, want %02X %02X", sei[4], sei[5], hevcSEIByte0, hevcSEIByte1)
	}
}

func TestManager_PendingCaptionSEI_EmitsContinuousNullSEI(t *testing.T) {
	m := NewManager()
	m.SetMode(ModeAuthor)

	for i := 0; i < 5; i++ {
		sei := m.PendingCaptionSEI("h264")
		if len(sei) == 0 {
			t.Fatalf("frame %d: PendingCaptionSEI() returned empty, want continuous null SEI for downstream decoder acquisition", i)
		}
	}
}

func TestManager_ModeTransition_NoStreamGap(t *testing.T) {
	m := NewManager()

	for i := 0; i < 3; i++ {
		if sei := m.PendingCaptionSEI("h264"); len(sei) == 0 {
			t.Fatalf("off mode frame %d: SEI was empty, want continuous carriage", i)
		}
	}

	m.SetMode(ModePassThrough)
	for i := 0; i < 3; i++ {
		if sei := m.PendingCaptionSEI("h264"); len(sei) == 0 {
			t.Fatalf("passthrough frame %d: SEI was empty after off->passthrough", i)
		}
	}

	m.SetMode(ModeAuthor)
	for i := 0; i < 3; i++ {
		if sei := m.PendingCaptionSEI("h264"); len(sei) == 0 {
			t.Fatalf("author frame %d: SEI was empty after passthrough->author", i)
		}
	}
}

func TestManager_PendingCaptionSEI_NilManagerSafe(t *testing.T) {
	var m *Manager
	if sei := m.PendingCaptionSEI("h264"); sei != nil {
		t.Errorf("nil manager PendingCaptionSEI() = %x, want nil", sei)
	}
}
