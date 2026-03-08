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

func TestAVC1ToAnnexBInto_MatchesAVC1ToAnnexB(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		avc1 []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"single_nalu", []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x01, 0x02, 0x03, 0x04}},
		{"multiple_nalus", []byte{
			0x00, 0x00, 0x00, 0x03, 0x67, 0xAA, 0xBB,
			0x00, 0x00, 0x00, 0x02, 0x65, 0xCC,
		}},
		{"three_nalus", []byte{
			0x00, 0x00, 0x00, 0x03, 0x67, 0xAA, 0xBB,
			0x00, 0x00, 0x00, 0x05, 0x68, 0x01, 0x02, 0x03, 0x04,
			0x00, 0x00, 0x00, 0x02, 0x65, 0xCC,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expected := AVC1ToAnnexB(tc.avc1)
			got := AVC1ToAnnexBInto(tc.avc1, nil)
			require.Equal(t, expected, got)
		})
	}
}

func TestAVC1ToAnnexBInto_ReusesBuffer(t *testing.T) {
	t.Parallel()
	avc1 := []byte{
		0x00, 0x00, 0x00, 0x03, 0x67, 0xAA, 0xBB,
		0x00, 0x00, 0x00, 0x02, 0x65, 0xCC,
	}

	buf := make([]byte, 0, 1024)
	result := AVC1ToAnnexBInto(avc1, buf[:0])
	require.Equal(t, AVC1ToAnnexB(avc1), result)

	result2 := AVC1ToAnnexBInto(avc1, result[:0])
	require.Equal(t, AVC1ToAnnexB(avc1), result2)
	require.Equal(t, cap(result), cap(result2), "backing array should be reused")
}

func TestAVC1ToAnnexBInto_NoAllocWhenCapSufficient(t *testing.T) {
	avc1 := []byte{
		0x00, 0x00, 0x00, 0x03, 0x67, 0xAA, 0xBB,
		0x00, 0x00, 0x00, 0x02, 0x65, 0xCC,
	}

	buf := make([]byte, 0, 256)
	allocs := testing.AllocsPerRun(100, func() {
		buf = AVC1ToAnnexBInto(avc1, buf[:0])
	})
	require.Equal(t, float64(0), allocs, "should not allocate when capacity is sufficient")
}

func TestAVC1ToAnnexBInto_NilDstAllocates(t *testing.T) {
	t.Parallel()
	avc1 := []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x01, 0x02, 0x03, 0x04}
	result := AVC1ToAnnexBInto(avc1, nil)
	expected := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x01, 0x02, 0x03, 0x04}
	require.Equal(t, expected, result)
}

func TestAVC1ToAnnexBInto_EmptyInput(t *testing.T) {
	t.Parallel()
	require.Nil(t, AVC1ToAnnexBInto(nil, nil))

	buf := make([]byte, 0, 64)
	result := AVC1ToAnnexBInto(nil, buf)
	require.NotNil(t, result)
	require.Empty(t, result)

	result = AVC1ToAnnexBInto([]byte{}, buf)
	require.NotNil(t, result)
	require.Empty(t, result)
}

func TestPrependSPSPPSInto_MatchesPrependSPSPPS(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		sps    []byte
		pps    []byte
		annexB []byte
	}{
		{
			"both_sps_pps",
			[]byte{0x67, 0x64, 0x00, 0x28},
			[]byte{0x68, 0xEE, 0x3C, 0x80},
			[]byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
		},
		{
			"only_sps",
			[]byte{0x67, 0x64},
			nil,
			[]byte{0x00, 0x00, 0x00, 0x01, 0x65},
		},
		{
			"only_pps",
			nil,
			[]byte{0x68, 0xEE},
			[]byte{0x00, 0x00, 0x00, 0x01, 0x65},
		},
		{
			"nil_both",
			nil,
			nil,
			[]byte{0x00, 0x00, 0x00, 0x01, 0x65},
		},
		{
			"empty_both",
			[]byte{},
			[]byte{},
			[]byte{0x00, 0x00, 0x00, 0x01, 0x65},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expected := PrependSPSPPS(tc.sps, tc.pps, tc.annexB)
			got := PrependSPSPPSInto(tc.sps, tc.pps, tc.annexB, nil)
			require.Equal(t, expected, got)
		})
	}
}

func TestPrependSPSPPSInto_ReusesBuffer(t *testing.T) {
	t.Parallel()
	sps := []byte{0x67, 0x64, 0x00, 0x28}
	pps := []byte{0x68, 0xEE, 0x3C, 0x80}
	annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88}

	buf := make([]byte, 0, 1024)
	result := PrependSPSPPSInto(sps, pps, annexB, buf[:0])
	require.Equal(t, PrependSPSPPS(sps, pps, annexB), result)

	result2 := PrependSPSPPSInto(sps, pps, annexB, result[:0])
	require.Equal(t, PrependSPSPPS(sps, pps, annexB), result2)
	require.Equal(t, cap(result), cap(result2), "backing array should be reused")
}

func TestPrependSPSPPSInto_NoAllocWhenCapSufficient(t *testing.T) {
	sps := []byte{0x67, 0x64, 0x00, 0x28}
	pps := []byte{0x68, 0xEE, 0x3C, 0x80}
	annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88}

	buf := make([]byte, 0, 1024)
	allocs := testing.AllocsPerRun(100, func() {
		buf = PrependSPSPPSInto(sps, pps, annexB, buf[:0])
	})
	require.Equal(t, float64(0), allocs, "should not allocate when capacity is sufficient")
}

func TestAnnexBToAVC1Into_MatchesAnnexBToAVC1(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		annexB []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"single_4byte", []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x01, 0x02, 0x03, 0x04}},
		{"single_3byte", []byte{0x00, 0x00, 0x01, 0x65, 0x01, 0x02}},
		{"multiple_4byte", []byte{
			0x00, 0x00, 0x00, 0x01, 0x67, 0xAA, 0xBB,
			0x00, 0x00, 0x00, 0x01, 0x65, 0xCC,
		}},
		{"mixed_start_codes", []byte{
			0x00, 0x00, 0x00, 0x01, 0x67, 0xAA, // 4-byte
			0x00, 0x00, 0x01, 0x65, 0xBB, // 3-byte
		}},
		{"three_nalus", []byte{
			0x00, 0x00, 0x00, 0x01, 0x67, 0xAA, 0xBB,
			0x00, 0x00, 0x00, 0x01, 0x68, 0xCC,
			0x00, 0x00, 0x00, 0x01, 0x65, 0xDD, 0xEE,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expected := AnnexBToAVC1(tc.annexB)
			got := AnnexBToAVC1Into(tc.annexB, nil)
			require.Equal(t, expected, got)
		})
	}
}

func TestAnnexBToAVC1Into_ReusesBuffer(t *testing.T) {
	t.Parallel()
	annexB := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0xAA, 0xBB,
		0x00, 0x00, 0x00, 0x01, 0x65, 0xCC,
	}

	buf := make([]byte, 0, 1024)
	result := AnnexBToAVC1Into(annexB, buf[:0])
	require.Equal(t, AnnexBToAVC1(annexB), result)

	// Second call should reuse the same backing array.
	result2 := AnnexBToAVC1Into(annexB, result[:0])
	require.Equal(t, AnnexBToAVC1(annexB), result2)
	require.Equal(t, cap(result), cap(result2), "backing array should be reused")
}

func TestAnnexBToAVC1Into_NilDst(t *testing.T) {
	t.Parallel()
	annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x01, 0x02}
	result := AnnexBToAVC1Into(annexB, nil)
	expected := []byte{0x00, 0x00, 0x00, 0x03, 0x65, 0x01, 0x02}
	require.Equal(t, expected, result)
}

func TestAnnexBToAVC1Into_EmptyInput(t *testing.T) {
	t.Parallel()
	require.Nil(t, AnnexBToAVC1Into(nil, nil))
	require.Nil(t, AnnexBToAVC1Into([]byte{}, nil))

	buf := make([]byte, 0, 64)
	require.Nil(t, AnnexBToAVC1Into(nil, buf))
	require.Nil(t, AnnexBToAVC1Into([]byte{}, buf))
}

func TestExtractNALUs_SubSlice(t *testing.T) {
	t.Parallel()
	avc1 := []byte{
		0x00, 0x00, 0x00, 0x03, 0x67, 0xAA, 0xBB,
		0x00, 0x00, 0x00, 0x02, 0x65, 0xCC,
	}
	nalus := ExtractNALUs(avc1)
	require.Len(t, nalus, 2)
	require.Equal(t, []byte{0x67, 0xAA, 0xBB}, nalus[0])
	require.Equal(t, []byte{0x65, 0xCC}, nalus[1])

	// Verify they are sub-slices of the input (share backing array).
	require.Equal(t, &avc1[4], &nalus[0][0], "first NALU should be a sub-slice of input")
	require.Equal(t, &avc1[11], &nalus[1][0], "second NALU should be a sub-slice of input")
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
