package codec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAVC1ToAnnexB_SingleNALU(t *testing.T) {
	t.Parallel()
	avc1 := []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x01, 0x02, 0x03, 0x04}
	annexB := AVC1ToAnnexB(avc1)
	expected := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x01, 0x02, 0x03, 0x04}
	require.Equal(t, expected, annexB)
}

func TestAVC1ToAnnexB_MultipleNALUs(t *testing.T) {
	t.Parallel()
	avc1 := []byte{
		0x00, 0x00, 0x00, 0x03, 0x67, 0xAA, 0xBB,
		0x00, 0x00, 0x00, 0x02, 0x65, 0xCC,
	}
	annexB := AVC1ToAnnexB(avc1)
	expected := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0xAA, 0xBB,
		0x00, 0x00, 0x00, 0x01, 0x65, 0xCC,
	}
	require.Equal(t, expected, annexB)
}

func TestAVC1ToAnnexB_EmptyInput(t *testing.T) {
	t.Parallel()
	annexB := AVC1ToAnnexB(nil)
	require.Nil(t, annexB)
	annexB = AVC1ToAnnexB([]byte{})
	require.Nil(t, annexB)
}

func TestAnnexBToAVC1_SingleNALU(t *testing.T) {
	t.Parallel()
	annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x01, 0x02, 0x03, 0x04}
	avc1 := AnnexBToAVC1(annexB)
	expected := []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x01, 0x02, 0x03, 0x04}
	require.Equal(t, expected, avc1)
}

func TestAnnexBToAVC1_MultipleNALUs(t *testing.T) {
	t.Parallel()
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0xAA, 0xBB,
		0x00, 0x00, 0x00, 0x01, 0x65, 0xCC,
	}
	avc1 := AnnexBToAVC1(annexB)
	expected := []byte{
		0x00, 0x00, 0x00, 0x03, 0x67, 0xAA, 0xBB,
		0x00, 0x00, 0x00, 0x02, 0x65, 0xCC,
	}
	require.Equal(t, expected, avc1)
}

func TestAnnexBToAVC1_ThreeByteStartCode(t *testing.T) {
	t.Parallel()
	annexB := []byte{0x00, 0x00, 0x01, 0x65, 0x01, 0x02}
	avc1 := AnnexBToAVC1(annexB)
	expected := []byte{0x00, 0x00, 0x00, 0x03, 0x65, 0x01, 0x02}
	require.Equal(t, expected, avc1)
}

func TestAnnexBToAVC1_EmptyInput(t *testing.T) {
	t.Parallel()
	avc1 := AnnexBToAVC1(nil)
	require.Nil(t, avc1)
}

func TestRoundTrip_AVC1_AnnexB_AVC1(t *testing.T) {
	t.Parallel()
	original := []byte{
		0x00, 0x00, 0x00, 0x03, 0x67, 0xAA, 0xBB,
		0x00, 0x00, 0x00, 0x05, 0x68, 0x01, 0x02, 0x03, 0x04,
		0x00, 0x00, 0x00, 0x02, 0x65, 0xCC,
	}
	annexB := AVC1ToAnnexB(original)
	roundTripped := AnnexBToAVC1(annexB)
	require.Equal(t, original, roundTripped)
}

func TestExtractNALUs(t *testing.T) {
	t.Parallel()
	avc1 := []byte{
		0x00, 0x00, 0x00, 0x03, 0x67, 0xAA, 0xBB,
		0x00, 0x00, 0x00, 0x02, 0x65, 0xCC,
	}
	nalus := ExtractNALUs(avc1)
	require.Len(t, nalus, 2)
	require.Equal(t, []byte{0x67, 0xAA, 0xBB}, nalus[0])
	require.Equal(t, []byte{0x65, 0xCC}, nalus[1])
}

func TestExtractNALUs_EmptyInput(t *testing.T) {
	t.Parallel()
	nalus := ExtractNALUs(nil)
	require.Nil(t, nalus)
	nalus = ExtractNALUs([]byte{})
	require.Nil(t, nalus)
}

func TestSplitAnnexBNALUs(t *testing.T) {
	t.Parallel()
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0xAA, 0xBB,
		0x00, 0x00, 0x00, 0x01, 0x65, 0xCC,
	}
	nalus := splitAnnexBNALUs(annexB)
	require.Len(t, nalus, 2)
	require.Equal(t, []byte{0x67, 0xAA, 0xBB}, nalus[0])
	require.Equal(t, []byte{0x65, 0xCC}, nalus[1])
}

func TestSplitAnnexBNALUs_ThreeByteStartCodes(t *testing.T) {
	t.Parallel()
	annexB := []byte{
		0x00, 0x00, 0x01, 0x67, 0xAA,
		0x00, 0x00, 0x01, 0x65, 0xBB,
	}
	nalus := splitAnnexBNALUs(annexB)
	require.Len(t, nalus, 2)
	require.Equal(t, []byte{0x67, 0xAA}, nalus[0])
	require.Equal(t, []byte{0x65, 0xBB}, nalus[1])
}

func TestSplitAnnexBNALUs_MixedStartCodes(t *testing.T) {
	t.Parallel()
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0xAA, // 4-byte start code
		0x00, 0x00, 0x01, 0x65, 0xBB, // 3-byte start code
	}
	nalus := splitAnnexBNALUs(annexB)
	require.Len(t, nalus, 2)
	require.Equal(t, []byte{0x67, 0xAA}, nalus[0])
	require.Equal(t, []byte{0x65, 0xBB}, nalus[1])
}

func TestParseSPSCodecString_HighProfile(t *testing.T) {
	t.Parallel()
	// SPS: nalu_type=0x67, profile_idc=0x64 (High), constraint=0x00, level=0x28 (4.0)
	sps := []byte{0x67, 0x64, 0x00, 0x28, 0xAC, 0xD9}
	result := ParseSPSCodecString(sps)
	require.Equal(t, "avc1.640028", result)
}

func TestParseSPSCodecString_BaselineProfile(t *testing.T) {
	t.Parallel()
	// SPS: nalu_type=0x67, profile_idc=0x42 (Baseline), constraint=0xC0, level=0x1E (3.0)
	sps := []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00}
	result := ParseSPSCodecString(sps)
	require.Equal(t, "avc1.42C01E", result)
}

func TestParseSPSCodecString_MainProfile(t *testing.T) {
	t.Parallel()
	// SPS: nalu_type=0x67, profile_idc=0x4D (Main), constraint=0x40, level=0x1F (3.1)
	sps := []byte{0x67, 0x4D, 0x40, 0x1F, 0xEC, 0xA0}
	result := ParseSPSCodecString(sps)
	require.Equal(t, "avc1.4D401F", result)
}

func TestParseSPSCodecString_TooShort(t *testing.T) {
	t.Parallel()
	// Fallback for short SPS
	require.Equal(t, "avc1.42C01E", ParseSPSCodecString(nil))
	require.Equal(t, "avc1.42C01E", ParseSPSCodecString([]byte{}))
	require.Equal(t, "avc1.42C01E", ParseSPSCodecString([]byte{0x67}))
	require.Equal(t, "avc1.42C01E", ParseSPSCodecString([]byte{0x67, 0x42, 0xC0}))
}

func TestPrependSPSPPS(t *testing.T) {
	t.Parallel()
	sps := []byte{0x67, 0x64, 0x00, 0x28}
	pps := []byte{0x68, 0xEE, 0x3C, 0x80}
	annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88}

	result := PrependSPSPPS(sps, pps, annexB)

	expected := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x28,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xEE, 0x3C, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88,
	}
	require.Equal(t, expected, result)
}

func TestPrependSPSPPS_NilSPS(t *testing.T) {
	t.Parallel()
	annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x65}
	result := PrependSPSPPS(nil, nil, annexB)
	require.Equal(t, annexB, result)
}

func TestPrependSPSPPS_EmptySPS(t *testing.T) {
	t.Parallel()
	annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x65}
	result := PrependSPSPPS([]byte{}, []byte{}, annexB)
	require.Equal(t, annexB, result)
}

func TestPrependSPSPPS_OnlySPS(t *testing.T) {
	t.Parallel()
	sps := []byte{0x67, 0x64}
	annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x65}
	result := PrependSPSPPS(sps, nil, annexB)
	expected := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x64,
		0x00, 0x00, 0x00, 0x01, 0x65,
	}
	require.Equal(t, expected, result)
}
