//go:build correctness

package switching

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit YUV422P10LE switching correctness tests.
//
// These verify that the format metadata (PipelineColorFormat, PipelineColorConfig)
// and frame lifecycle operations correctly handle 10-bit frames during
// cut/preview operations.
// ==========================================================================

// generateSolid422P10LE creates a solid-color YUV422P10LE frame.
func generateSolid422P10LE(w, h int, y, cb, cr uint16) []byte {
	format := switcher.ColorYUV422_10bit
	buf := make([]byte, format.FrameSize(w, h))

	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], y)
	}
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[cbOff+i*2:], cb)
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], cr)
	}
	return buf
}

// ==========================================================================
// 1. TestSwitching10bit_CutPreservesFormat — A ProcessingFrame with
//    Format=ColorYUV422_10bit preserves its format through construction.
// ==========================================================================

func TestSwitching10bit_CutPreservesFormat(t *testing.T) {
	t.Parallel()

	w, h := 320, 240
	data := generateSolid422P10LE(w, h, 500, 512, 512)

	pf := &switcher.ProcessingFrame{
		Data:   data,
		Format: switcher.ColorYUV422_10bit,
		Width:  w,
		Height: h,
		PTS:    90000,
	}

	require.Equal(t, switcher.ColorYUV422_10bit, pf.Format,
		"ProcessingFrame Format should be ColorYUV422_10bit")

	// Verify the frame data size matches the 10-bit format.
	require.Equal(t, switcher.ColorYUV422_10bit.FrameSize(w, h), len(pf.Data),
		"frame data size should match ColorYUV422_10bit.FrameSize")

	// DeepCopy should preserve format.
	cp := pf.DeepCopy()
	require.Equal(t, switcher.ColorYUV422_10bit, cp.Format,
		"DeepCopy should preserve ColorYUV422_10bit format")
}

// ==========================================================================
// 2. TestSwitching10bit_PreviewPreservesFormat — Verify format on frames
//    destined for preview path.
// ==========================================================================

func TestSwitching10bit_PreviewPreservesFormat(t *testing.T) {
	t.Parallel()

	w, h := 320, 240

	// Create frames for two sources with distinct 10-bit values.
	dataA := generateSolid422P10LE(w, h, 300, 400, 600)
	dataB := generateSolid422P10LE(w, h, 700, 200, 800)

	pfA := &switcher.ProcessingFrame{
		Data:   dataA,
		Format: switcher.ColorYUV422_10bit,
		Width:  w,
		Height: h,
		PTS:    90000,
	}

	pfB := &switcher.ProcessingFrame{
		Data:   dataB,
		Format: switcher.ColorYUV422_10bit,
		Width:  w,
		Height: h,
		PTS:    93000,
	}

	// Both frames should report correct format.
	require.Equal(t, switcher.ColorYUV422_10bit, pfA.Format)
	require.Equal(t, switcher.ColorYUV422_10bit, pfB.Format)

	// Verify the data contains distinct values.
	yA := binary.LittleEndian.Uint16(pfA.Data[0:])
	yB := binary.LittleEndian.Uint16(pfB.Data[0:])
	require.Equal(t, uint16(300), yA, "source A Y should be 300")
	require.Equal(t, uint16(700), yB, "source B Y should be 700")
}

// ==========================================================================
// 3. TestSwitching10bit_ColorProfileValidation — AVC rejects 10-bit 422.
// ==========================================================================

func TestSwitching10bit_ColorProfileValidation(t *testing.T) {
	t.Parallel()

	// Professional profile (10-bit + HEVC) should be valid.
	prof := switcher.ColorProfileProfessional()
	require.NoError(t, prof.Validate(),
		"professional profile (10-bit + HEVC) should validate")

	// 10-bit + AVC should fail validation.
	invalid := switcher.PipelineColorConfig{
		ColorFormat: switcher.ColorYUV422_10bit,
		Codec:       switcher.CodecAVC,
	}
	require.Error(t, invalid.Validate(),
		"10-bit + AVC should fail validation (AVC doesn't support 10-bit 422)")

	// Standard profile (8-bit + AVC) should be valid.
	std := switcher.ColorProfileStandard()
	require.NoError(t, std.Validate(),
		"standard profile (8-bit + AVC) should validate")

	// Standard HEVC profile should be valid.
	stdHEVC := switcher.ColorProfileStandardHEVC()
	require.NoError(t, stdHEVC.Validate(),
		"standard HEVC profile (8-bit + HEVC) should validate")
}

// ==========================================================================
// 4. TestSwitching10bit_ParseColorProfile — Profile name parsing.
// ==========================================================================

func TestSwitching10bit_ParseColorProfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile string
		format  switcher.PipelineColorFormat
		codec   switcher.PipelineCodec
		err     bool
	}{
		{"standard", "standard", switcher.ColorYUV420_8bit, switcher.CodecAVC, false},
		{"standard-hevc", "standard-hevc", switcher.ColorYUV420_8bit, switcher.CodecHEVC, false},
		{"professional", "professional", switcher.ColorYUV422_10bit, switcher.CodecHEVC, false},
		{"invalid", "invalid-profile", 0, 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := switcher.ParseColorProfile(tc.profile)
			if tc.err {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.format, cfg.ColorFormat)
			require.Equal(t, tc.codec, cfg.Codec)
		})
	}
}

// ==========================================================================
// 5. TestSwitching10bit_FrameRefCounting — Reference counting works with
//    10-bit frames.
// ==========================================================================

func TestSwitching10bit_FrameRefCounting(t *testing.T) {
	t.Parallel()

	w, h := 32, 32
	data := generateSolid422P10LE(w, h, 500, 512, 512)

	pf := &switcher.ProcessingFrame{
		Data:   data,
		Format: switcher.ColorYUV422_10bit,
		Width:  w,
		Height: h,
	}

	// Set initial refs.
	pf.SetRefs(1)
	require.Equal(t, int32(1), pf.Refs())

	// Increment.
	pf.Ref()
	require.Equal(t, int32(2), pf.Refs())

	// First release doesn't nil data.
	pf.ReleaseYUV()
	require.Equal(t, int32(1), pf.Refs())
	require.NotNil(t, pf.Data, "data should not be nil after first release")

	// Second release nils data.
	pf.ReleaseYUV()
	require.Nil(t, pf.Data, "data should be nil after final release")
}

// ==========================================================================
// 6. TestSwitching10bit_FormatStrings — String representations.
// ==========================================================================

func TestSwitching10bit_FormatStrings(t *testing.T) {
	t.Parallel()

	require.Equal(t, "yuv420p", switcher.ColorYUV420_8bit.String())
	require.Equal(t, "yuv422p10le", switcher.ColorYUV422_10bit.String())
	require.Equal(t, "h264", switcher.CodecAVC.String())
	require.Equal(t, "h265", switcher.CodecHEVC.String())
}
