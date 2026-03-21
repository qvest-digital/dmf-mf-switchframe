package stmap

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- Test PNG helpers ---

func makeTestPNG16(w, h int) []byte {
	img := image.NewNRGBA64(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r := uint16(float64(x) / float64(w) * 65535)
			g := uint16(float64(y) / float64(h) * 65535)
			img.SetNRGBA64(x, y, color.NRGBA64{R: r, G: g, B: 0, A: 65535})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func makeTestPNG8(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r := uint8(float64(x) / float64(w) * 255)
			g := uint8(float64(y) / float64(h) * 255)
			img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// --- PNG tests ---

func TestReadPNG_16bit(t *testing.T) {
	const w, h = 4, 2
	data := makeTestPNG16(w, h)

	m, err := ReadPNG(data, "test16")
	require.NoError(t, err)
	require.Equal(t, "test16", m.Name)
	require.Equal(t, w, m.Width)
	require.Equal(t, h, m.Height)
	require.Len(t, m.S, w*h)
	require.Len(t, m.T, w*h)

	// Verify S (Red channel) = x/w, T (Green channel) = y/h.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			expectS := float64(uint16(float64(x)/float64(w)*65535)) / 65535.0
			expectT := float64(uint16(float64(y)/float64(h)*65535)) / 65535.0
			require.InDelta(t, expectS, float64(m.S[idx]), 1e-4,
				"S mismatch at (%d,%d)", x, y)
			require.InDelta(t, expectT, float64(m.T[idx]), 1e-4,
				"T mismatch at (%d,%d)", x, y)
		}
	}
}

func TestReadPNG_8bit(t *testing.T) {
	const w, h = 4, 2
	data := makeTestPNG8(w, h)

	m, err := ReadPNG(data, "test8")
	require.NoError(t, err)
	require.Equal(t, "test8", m.Name)
	require.Equal(t, w, m.Width)
	require.Equal(t, h, m.Height)
	require.Len(t, m.S, w*h)
	require.Len(t, m.T, w*h)

	// Verify S (Red channel) = x/w, T (Green channel) = y/h with 8-bit precision.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			expectS := float64(uint8(float64(x)/float64(w)*255)) / 255.0
			expectT := float64(uint8(float64(y)/float64(h)*255)) / 255.0
			require.InDelta(t, expectS, float64(m.S[idx]), 1e-3,
				"S mismatch at (%d,%d)", x, y)
			require.InDelta(t, expectT, float64(m.T[idx]), 1e-3,
				"T mismatch at (%d,%d)", x, y)
		}
	}
}

func TestReadPNG_OddDimensions(t *testing.T) {
	// 3x2: odd width.
	data := makeTestPNG16(3, 2)
	_, err := ReadPNG(data, "odd")
	require.ErrorIs(t, err, ErrInvalidDimensions)

	// 4x3: odd height.
	data = makeTestPNG16(4, 3)
	_, err = ReadPNG(data, "odd")
	require.ErrorIs(t, err, ErrInvalidDimensions)
}

func TestReadPNG_InvalidData(t *testing.T) {
	_, err := ReadPNG([]byte("not a png"), "bad")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrInvalidDimensions)
}

// --- Raw format tests ---

func TestReadWriteRaw_RoundTrip(t *testing.T) {
	m, err := NewSTMap("roundtrip", 4, 2)
	require.NoError(t, err)

	// Fill with known values.
	for i := range m.S {
		m.S[i] = float32(i) / float32(len(m.S))
		m.T[i] = 1.0 - float32(i)/float32(len(m.T))
	}

	data, err := WriteRaw(m)
	require.NoError(t, err)

	// Expected size: 8 (header) + w*h*4 (S) + w*h*4 (T).
	expectedLen := 8 + 4*2*4*2
	require.Len(t, data, expectedLen)

	got, err := ReadRaw(data, "roundtrip")
	require.NoError(t, err)
	require.Equal(t, "roundtrip", got.Name)
	require.Equal(t, 4, got.Width)
	require.Equal(t, 2, got.Height)
	require.Len(t, got.S, 4*2)
	require.Len(t, got.T, 4*2)

	// Values must match exactly (no precision loss in float32 round-trip).
	for i := range m.S {
		require.Equal(t, m.S[i], got.S[i], "S mismatch at index %d", i)
		require.Equal(t, m.T[i], got.T[i], "T mismatch at index %d", i)
	}
}

func TestReadRaw_TooShort(t *testing.T) {
	// Valid header but truncated data.
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(4))
	_ = binary.Write(&buf, binary.BigEndian, uint32(2))
	// Need 4*2*4*2 = 64 more bytes, but only write 10.
	buf.Write(make([]byte, 10))

	_, err := ReadRaw(buf.Bytes(), "short")
	require.Error(t, err)

	// Also too short for header.
	_, err = ReadRaw([]byte{0, 0, 0, 4}, "short")
	require.Error(t, err)
}

func TestReadRaw_OddDimensions(t *testing.T) {
	var buf bytes.Buffer
	// 3x2: odd width.
	_ = binary.Write(&buf, binary.BigEndian, uint32(3))
	_ = binary.Write(&buf, binary.BigEndian, uint32(2))
	buf.Write(make([]byte, 3*2*4*2))

	_, err := ReadRaw(buf.Bytes(), "odd")
	require.ErrorIs(t, err, ErrInvalidDimensions)
}

func TestReadRaw_InvalidHeader(t *testing.T) {
	// Zero width.
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(0))
	_ = binary.Write(&buf, binary.BigEndian, uint32(2))

	_, err := ReadRaw(buf.Bytes(), "zero-w")
	require.ErrorIs(t, err, ErrInvalidDimensions)

	// Zero height.
	buf.Reset()
	_ = binary.Write(&buf, binary.BigEndian, uint32(4))
	_ = binary.Write(&buf, binary.BigEndian, uint32(0))

	_, err = ReadRaw(buf.Bytes(), "zero-h")
	require.ErrorIs(t, err, ErrInvalidDimensions)
}

func TestWriteRaw_HeaderEncoding(t *testing.T) {
	m, err := NewSTMap("hdr", 8, 6)
	require.NoError(t, err)

	data, err := WriteRaw(m)
	require.NoError(t, err)

	// Verify header is BigEndian.
	w := binary.BigEndian.Uint32(data[0:4])
	h := binary.BigEndian.Uint32(data[4:8])
	require.Equal(t, uint32(8), w)
	require.Equal(t, uint32(6), h)
}

func TestWriteRaw_FloatEncoding(t *testing.T) {
	m, err := NewSTMap("enc", 2, 2)
	require.NoError(t, err)

	m.S[0] = 0.5
	m.T[0] = 0.75

	data, err := WriteRaw(m)
	require.NoError(t, err)

	// S plane starts at offset 8, T plane at 8 + 2*2*4 = 24.
	sVal := math.Float32frombits(binary.LittleEndian.Uint32(data[8:12]))
	tVal := math.Float32frombits(binary.LittleEndian.Uint32(data[24:28]))
	require.Equal(t, float32(0.5), sVal)
	require.Equal(t, float32(0.75), tVal)
}

// --- PNG with generic image type (RGBA64, not NRGBA64) ---

func TestReadPNG_RGBA64(t *testing.T) {
	// Use image.RGBA64 to exercise the generic fallback path.
	const w, h = 4, 2
	img := image.NewRGBA64(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r := uint16(float64(x) / float64(w) * 65535)
			g := uint16(float64(y) / float64(h) * 65535)
			img.SetRGBA64(x, y, color.RGBA64{R: r, G: g, B: 0, A: 65535})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)

	m, err := ReadPNG(buf.Bytes(), "rgba64")
	require.NoError(t, err)
	require.Equal(t, w, m.Width)
	require.Equal(t, h, m.Height)

	// Spot-check a few values.
	// (0,0): S=0/4=0, T=0/2=0
	require.InDelta(t, 0.0, float64(m.S[0]), 1e-4)
	require.InDelta(t, 0.0, float64(m.T[0]), 1e-4)

	// (3,1): S=3/4*65535/65535, T=1/2*65535/65535
	require.InDelta(t, 0.75, float64(m.S[w+3]), 2e-2)
	require.InDelta(t, 0.5, float64(m.T[w+3]), 2e-2)
}
