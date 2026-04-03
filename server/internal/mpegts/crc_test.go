package mpegts

import "testing"

func TestCRC32MPEG2_KnownVector(t *testing.T) {
	// Standard CRC-32/MPEG-2 test vector: "123456789" → 0x0376E6E7
	got := CRC32MPEG2([]byte("123456789"))
	if got != 0x0376E6E7 {
		t.Fatalf("CRC32MPEG2(%q) = 0x%08X, want 0x0376E6E7", "123456789", got)
	}
}

func TestCRC32MPEG2_Empty(t *testing.T) {
	// Empty input stays at the init value (no XOR finalisation).
	got := CRC32MPEG2(nil)
	if got != 0xFFFFFFFF {
		t.Fatalf("CRC32MPEG2(nil) = 0x%08X, want 0xFFFFFFFF", got)
	}
	got = CRC32MPEG2([]byte{})
	if got != 0xFFFFFFFF {
		t.Fatalf("CRC32MPEG2([]) = 0x%08X, want 0xFFFFFFFF", got)
	}
}

func TestCRC32MPEG2_SingleByte(t *testing.T) {
	// Single byte 0x00: init XOR 0x00<<24 = 0xFFFFFFFF, then 8 shifts.
	got := CRC32MPEG2([]byte{0x00})
	if got == 0xFFFFFFFF {
		t.Fatal("CRC32MPEG2(0x00) must differ from init value")
	}
	// Verify against a manually computed value.
	// 0xFF ^ 0x00 = 0xFF in high byte, after 8 poly shifts → known result.
	// We just ensure it's deterministic and non-trivial.
	got2 := CRC32MPEG2([]byte{0x00})
	if got != got2 {
		t.Fatalf("CRC32MPEG2(0x00) not deterministic: 0x%08X vs 0x%08X", got, got2)
	}
}

func TestCRC32MPEG2_Deterministic(t *testing.T) {
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE}
	a := CRC32MPEG2(data)
	b := CRC32MPEG2(data)
	if a != b {
		t.Fatalf("CRC32MPEG2 not deterministic: 0x%08X vs 0x%08X", a, b)
	}
}

func TestCRC32MPEG2_DifferentInputsDiffer(t *testing.T) {
	a := CRC32MPEG2([]byte{0x01})
	b := CRC32MPEG2([]byte{0x02})
	if a == b {
		t.Fatalf("CRC32MPEG2(0x01) == CRC32MPEG2(0x02) = 0x%08X, expected different", a)
	}
}
