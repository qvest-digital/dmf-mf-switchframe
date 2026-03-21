package stmap

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHalfToFloat(t *testing.T) {
	tests := []struct {
		name string
		h    uint16
		want float32
	}{
		{"positive zero", 0x0000, 0.0},
		{"negative zero", 0x8000, float32(math.Copysign(0, -1))},
		{"one", 0x3C00, 1.0},
		{"negative one", 0xBC00, -1.0},
		{"half", 0x3800, 0.5},
		{"two", 0x4000, 2.0},
		{"smallest normal", 0x0400, float32(math.Ldexp(1, -14))},              // 2^-14
		{"largest subnormal", 0x03FF, float32(math.Ldexp(1, -14) * 1023 / 1024)}, // just below 2^-14
		{"smallest subnormal", 0x0001, float32(math.Ldexp(1, -24))},           // 2^-24
		{"positive infinity", 0x7C00, float32(math.Inf(1))},
		{"negative infinity", 0xFC00, float32(math.Inf(-1))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := halfToFloat(tt.h)
			if math.IsInf(float64(tt.want), 0) {
				require.True(t, math.IsInf(float64(got), int(math.Copysign(1, float64(tt.want)))),
					"expected %v, got %v", tt.want, got)
			} else {
				require.InDelta(t, float64(tt.want), float64(got), 1e-7,
					"h=0x%04X: expected %v, got %v", tt.h, tt.want, got)
			}
		})
	}

	// NaN test separately (NaN != NaN)
	t.Run("NaN", func(t *testing.T) {
		got := halfToFloat(0x7E00) // quiet NaN
		require.True(t, math.IsNaN(float64(got)), "expected NaN, got %v", got)
	})
}

// buildSyntheticEXR constructs a minimal valid uncompressed EXR file with
// R and G float32 channels of the given dimensions and pixel values.
// rVals and gVals must have length width*height in row-major order.
func buildSyntheticEXR(t *testing.T, width, height int, rVals, gVals []float32, compression byte) []byte {
	t.Helper()
	require.Equal(t, width*height, len(rVals))
	require.Equal(t, width*height, len(gVals))

	var buf bytes.Buffer

	// Magic number
	binary.Write(&buf, binary.LittleEndian, uint32(0x762F3101))
	// Version: 2, scanline (no flags in bits 9-31)
	binary.Write(&buf, binary.LittleEndian, uint32(2))

	// --- Header attributes ---

	// channels attribute (chlist type)
	// Channels must be in alphabetical order: G, R
	writeAttr := func(name, typeName string, data []byte) {
		buf.WriteString(name)
		buf.WriteByte(0) // null terminator for name
		buf.WriteString(typeName)
		buf.WriteByte(0) // null terminator for type name
		binary.Write(&buf, binary.LittleEndian, int32(len(data)))
		buf.Write(data)
	}

	// Build channel list data: G(float,1,1) then R(float,1,1) then \0
	var chData bytes.Buffer
	// Channel G
	chData.WriteString("G")
	chData.WriteByte(0)
	binary.Write(&chData, binary.LittleEndian, int32(2)) // FLOAT
	chData.WriteByte(0)                                   // pLinear
	chData.Write([]byte{0, 0, 0})                         // reserved
	binary.Write(&chData, binary.LittleEndian, int32(1))  // xSampling
	binary.Write(&chData, binary.LittleEndian, int32(1))  // ySampling
	// Channel R
	chData.WriteString("R")
	chData.WriteByte(0)
	binary.Write(&chData, binary.LittleEndian, int32(2)) // FLOAT
	chData.WriteByte(0)                                   // pLinear
	chData.Write([]byte{0, 0, 0})                         // reserved
	binary.Write(&chData, binary.LittleEndian, int32(1))  // xSampling
	binary.Write(&chData, binary.LittleEndian, int32(1))  // ySampling
	// Terminating null byte for channel list
	chData.WriteByte(0)

	writeAttr("channels", "chlist", chData.Bytes())

	// compression attribute
	writeAttr("compression", "compression", []byte{compression})

	// dataWindow attribute (box2i: xMin, yMin, xMax, yMax)
	var dwData bytes.Buffer
	binary.Write(&dwData, binary.LittleEndian, int32(0))
	binary.Write(&dwData, binary.LittleEndian, int32(0))
	binary.Write(&dwData, binary.LittleEndian, int32(width-1))
	binary.Write(&dwData, binary.LittleEndian, int32(height-1))
	writeAttr("dataWindow", "box2i", dwData.Bytes())

	// displayWindow attribute
	writeAttr("displayWindow", "box2i", dwData.Bytes())

	// lineOrder attribute
	writeAttr("lineOrder", "lineOrder", []byte{0}) // INCREASING_Y

	// End of header
	buf.WriteByte(0)

	// --- Offset table ---
	// For uncompressed: one entry per scanline
	// For ZIP: one entry per 16-scanline block
	var scanlineBlockCount int
	var scanlineRowsPerBlock int
	switch compression {
	case 0: // NO_COMPRESSION
		scanlineBlockCount = height
		scanlineRowsPerBlock = 1
	case 2: // ZIPS_COMPRESSION
		scanlineBlockCount = height
		scanlineRowsPerBlock = 1
	case 3: // ZIP_COMPRESSION
		scanlineBlockCount = (height + 15) / 16
		scanlineRowsPerBlock = 16
	default:
		t.Fatalf("unsupported compression for test builder: %d", compression)
	}

	offsetTableStart := buf.Len()
	// Reserve space for offset table
	offsets := make([]int64, scanlineBlockCount)
	for range offsets {
		binary.Write(&buf, binary.LittleEndian, int64(0)) // placeholder
	}

	// --- Scanline blocks ---

	for block := 0; block < scanlineBlockCount; block++ {
		startY := block * scanlineRowsPerBlock
		endY := startY + scanlineRowsPerBlock
		if endY > height {
			endY = height
		}

		offsets[block] = int64(buf.Len())

		// y-coordinate of first scanline in this block
		binary.Write(&buf, binary.LittleEndian, int32(startY))

		// Build pixel data for this block: channels in alphabetical order (G, R)
		// Each channel's pixels for all rows in the block are contiguous.
		var pixelData bytes.Buffer
		for row := startY; row < endY; row++ {
			for x := 0; x < width; x++ {
				binary.Write(&pixelData, binary.LittleEndian, gVals[row*width+x])
			}
		}
		for row := startY; row < endY; row++ {
			for x := 0; x < width; x++ {
				binary.Write(&pixelData, binary.LittleEndian, rVals[row*width+x])
			}
		}

		rawPixels := pixelData.Bytes()

		if compression == 0 {
			// Uncompressed: write size then data
			binary.Write(&buf, binary.LittleEndian, int32(len(rawPixels)))
			buf.Write(rawPixels)
		} else {
			// ZIPS or ZIP: zlib compress
			var compressed bytes.Buffer
			w, err := zlib.NewWriterLevel(&compressed, zlib.DefaultCompression)
			require.NoError(t, err)
			_, err = w.Write(rawPixels)
			require.NoError(t, err)
			require.NoError(t, w.Close())

			binary.Write(&buf, binary.LittleEndian, int32(compressed.Len()))
			buf.Write(compressed.Bytes())
		}
	}

	// Patch offset table
	data := buf.Bytes()
	for i, off := range offsets {
		binary.LittleEndian.PutUint64(data[offsetTableStart+i*8:], uint64(off))
	}

	return data
}

func TestReadEXR_InvalidMagic(t *testing.T) {
	data := []byte("not an EXR file at all")
	_, err := ReadEXR(data, "test")
	require.Error(t, err)
	require.Contains(t, err.Error(), "magic")
}

func TestReadEXR_TruncatedHeader(t *testing.T) {
	// Valid magic but truncated after version
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data[0:4], 0x762F3101)
	binary.LittleEndian.PutUint32(data[4:8], 2)

	_, err := ReadEXR(data, "test")
	require.Error(t, err)
}

func TestReadEXR_TiledNotSupported(t *testing.T) {
	// Version with tiled flag set (bit 9)
	data := make([]byte, 100)
	binary.LittleEndian.PutUint32(data[0:4], 0x762F3101)
	binary.LittleEndian.PutUint32(data[4:8], 2|0x200) // bit 9 = tiled

	_, err := ReadEXR(data, "test")
	require.Error(t, err)
	require.Contains(t, err.Error(), "tiled")
}

func TestReadEXR_SyntheticUncompressed(t *testing.T) {
	const w, h = 4, 2

	// Known R (S) and G (T) values
	rVals := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8}
	gVals := []float32{0.9, 0.8, 0.7, 0.6, 0.5, 0.4, 0.3, 0.2}

	data := buildSyntheticEXR(t, w, h, rVals, gVals, 0) // NO_COMPRESSION

	m, err := ReadEXR(data, "test-uncompressed")
	require.NoError(t, err)
	require.Equal(t, "test-uncompressed", m.Name)
	require.Equal(t, w, m.Width)
	require.Equal(t, h, m.Height)

	// R channel → S, G channel → T
	for i := 0; i < w*h; i++ {
		require.InDelta(t, rVals[i], m.S[i], 1e-6, "S[%d] mismatch", i)
		require.InDelta(t, gVals[i], m.T[i], 1e-6, "T[%d] mismatch", i)
	}
}

func TestReadEXR_SyntheticZIPS(t *testing.T) {
	const w, h = 4, 2

	rVals := []float32{0.0, 0.25, 0.5, 0.75, 1.0, 0.9, 0.8, 0.7}
	gVals := []float32{1.0, 0.75, 0.5, 0.25, 0.0, 0.1, 0.2, 0.3}

	data := buildSyntheticEXR(t, w, h, rVals, gVals, 2) // ZIPS_COMPRESSION

	m, err := ReadEXR(data, "test-zips")
	require.NoError(t, err)
	require.Equal(t, w, m.Width)
	require.Equal(t, h, m.Height)

	for i := 0; i < w*h; i++ {
		require.InDelta(t, rVals[i], m.S[i], 1e-6, "S[%d] mismatch", i)
		require.InDelta(t, gVals[i], m.T[i], 1e-6, "T[%d] mismatch", i)
	}
}

func TestReadEXR_SyntheticZIP(t *testing.T) {
	// Use a height that spans multiple 16-row blocks
	const w, h = 4, 34 // 34 rows = 3 blocks (16 + 16 + 2)

	rVals := make([]float32, w*h)
	gVals := make([]float32, w*h)
	for i := range rVals {
		rVals[i] = float32(i) / float32(w*h)
		gVals[i] = 1.0 - float32(i)/float32(w*h)
	}

	data := buildSyntheticEXR(t, w, h, rVals, gVals, 3) // ZIP_COMPRESSION

	m, err := ReadEXR(data, "test-zip")
	require.NoError(t, err)
	require.Equal(t, w, m.Width)
	require.Equal(t, h, m.Height)

	for i := 0; i < w*h; i++ {
		require.InDelta(t, rVals[i], m.S[i], 1e-6, "S[%d] mismatch", i)
		require.InDelta(t, gVals[i], m.T[i], 1e-6, "T[%d] mismatch", i)
	}
}

func TestReadEXR_OddDimensions(t *testing.T) {
	// 3x2 image — odd width should fail
	rVals := make([]float32, 3*2)
	gVals := make([]float32, 3*2)
	for i := range rVals {
		rVals[i] = float32(i) / 6.0
		gVals[i] = float32(i) / 6.0
	}

	data := buildSyntheticEXROddDims(t, 3, 2, rVals, gVals)
	_, err := ReadEXR(data, "odd-test")
	require.ErrorIs(t, err, ErrInvalidDimensions)
}

func TestReadEXR_HalfFloatChannels(t *testing.T) {
	// Build a synthetic EXR with half-float (type=1) channels
	const w, h = 4, 2

	rVals := []float32{0.0, 0.5, 1.0, -1.0, 0.25, 0.75, 2.0, 0.125}
	gVals := []float32{1.0, 0.5, 0.0, -1.0, 0.75, 0.25, 2.0, 0.875}

	data := buildSyntheticEXRHalf(t, w, h, rVals, gVals)

	m, err := ReadEXR(data, "test-half")
	require.NoError(t, err)
	require.Equal(t, w, m.Width)
	require.Equal(t, h, m.Height)

	// Half-float precision is ~3 decimal digits
	for i := 0; i < w*h; i++ {
		require.InDelta(t, rVals[i], m.S[i], 0.002, "S[%d] mismatch", i)
		require.InDelta(t, gVals[i], m.T[i], 0.002, "T[%d] mismatch", i)
	}
}

func TestReadEXR_MissingChannels(t *testing.T) {
	// Build an EXR with only a B channel — no R or G
	data := buildSyntheticEXRSingleChannel(t, 4, 2, "B")
	_, err := ReadEXR(data, "no-rg")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing")
}

func TestReadEXR_UnsupportedCompression(t *testing.T) {
	// Build an EXR with compression type 4 (PIZ)
	data := buildSyntheticEXRWithCompression(t, 4, 2, 4)
	_, err := ReadEXR(data, "piz")
	require.Error(t, err)
	require.Contains(t, err.Error(), "compression")
}

// --- Helper builders for special test cases ---

// buildSyntheticEXROddDims builds a minimal EXR with arbitrary (possibly odd) dimensions.
// Same as buildSyntheticEXR but doesn't check even dims.
func buildSyntheticEXROddDims(t *testing.T, width, height int, rVals, gVals []float32) []byte {
	t.Helper()
	require.Equal(t, width*height, len(rVals))
	require.Equal(t, width*height, len(gVals))

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(0x762F3101))
	binary.Write(&buf, binary.LittleEndian, uint32(2))

	writeAttr := func(name, typeName string, data []byte) {
		buf.WriteString(name)
		buf.WriteByte(0)
		buf.WriteString(typeName)
		buf.WriteByte(0)
		binary.Write(&buf, binary.LittleEndian, int32(len(data)))
		buf.Write(data)
	}

	var chData bytes.Buffer
	chData.WriteString("G")
	chData.WriteByte(0)
	binary.Write(&chData, binary.LittleEndian, int32(2))
	chData.WriteByte(0)
	chData.Write([]byte{0, 0, 0})
	binary.Write(&chData, binary.LittleEndian, int32(1))
	binary.Write(&chData, binary.LittleEndian, int32(1))
	chData.WriteString("R")
	chData.WriteByte(0)
	binary.Write(&chData, binary.LittleEndian, int32(2))
	chData.WriteByte(0)
	chData.Write([]byte{0, 0, 0})
	binary.Write(&chData, binary.LittleEndian, int32(1))
	binary.Write(&chData, binary.LittleEndian, int32(1))
	chData.WriteByte(0)
	writeAttr("channels", "chlist", chData.Bytes())

	writeAttr("compression", "compression", []byte{0})

	var dwData bytes.Buffer
	binary.Write(&dwData, binary.LittleEndian, int32(0))
	binary.Write(&dwData, binary.LittleEndian, int32(0))
	binary.Write(&dwData, binary.LittleEndian, int32(width-1))
	binary.Write(&dwData, binary.LittleEndian, int32(height-1))
	writeAttr("dataWindow", "box2i", dwData.Bytes())
	writeAttr("displayWindow", "box2i", dwData.Bytes())
	writeAttr("lineOrder", "lineOrder", []byte{0})
	buf.WriteByte(0)

	offsetTableStart := buf.Len()
	offsets := make([]int64, height)
	for range offsets {
		binary.Write(&buf, binary.LittleEndian, int64(0))
	}

	for y := 0; y < height; y++ {
		offsets[y] = int64(buf.Len())
		binary.Write(&buf, binary.LittleEndian, int32(y))

		var pixelData bytes.Buffer
		for x := 0; x < width; x++ {
			binary.Write(&pixelData, binary.LittleEndian, gVals[y*width+x])
		}
		for x := 0; x < width; x++ {
			binary.Write(&pixelData, binary.LittleEndian, rVals[y*width+x])
		}

		rawPixels := pixelData.Bytes()
		binary.Write(&buf, binary.LittleEndian, int32(len(rawPixels)))
		buf.Write(rawPixels)
	}

	data := buf.Bytes()
	for i, off := range offsets {
		binary.LittleEndian.PutUint64(data[offsetTableStart+i*8:], uint64(off))
	}
	return data
}

// buildSyntheticEXRHalf builds an EXR with half-float (type=1) R and G channels.
func buildSyntheticEXRHalf(t *testing.T, width, height int, rVals, gVals []float32) []byte {
	t.Helper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(0x762F3101))
	binary.Write(&buf, binary.LittleEndian, uint32(2))

	writeAttr := func(name, typeName string, data []byte) {
		buf.WriteString(name)
		buf.WriteByte(0)
		buf.WriteString(typeName)
		buf.WriteByte(0)
		binary.Write(&buf, binary.LittleEndian, int32(len(data)))
		buf.Write(data)
	}

	var chData bytes.Buffer
	// G channel as HALF
	chData.WriteString("G")
	chData.WriteByte(0)
	binary.Write(&chData, binary.LittleEndian, int32(1)) // HALF
	chData.WriteByte(0)
	chData.Write([]byte{0, 0, 0})
	binary.Write(&chData, binary.LittleEndian, int32(1))
	binary.Write(&chData, binary.LittleEndian, int32(1))
	// R channel as HALF
	chData.WriteString("R")
	chData.WriteByte(0)
	binary.Write(&chData, binary.LittleEndian, int32(1)) // HALF
	chData.WriteByte(0)
	chData.Write([]byte{0, 0, 0})
	binary.Write(&chData, binary.LittleEndian, int32(1))
	binary.Write(&chData, binary.LittleEndian, int32(1))
	chData.WriteByte(0)
	writeAttr("channels", "chlist", chData.Bytes())

	writeAttr("compression", "compression", []byte{0})

	var dwData bytes.Buffer
	binary.Write(&dwData, binary.LittleEndian, int32(0))
	binary.Write(&dwData, binary.LittleEndian, int32(0))
	binary.Write(&dwData, binary.LittleEndian, int32(width-1))
	binary.Write(&dwData, binary.LittleEndian, int32(height-1))
	writeAttr("dataWindow", "box2i", dwData.Bytes())
	writeAttr("displayWindow", "box2i", dwData.Bytes())
	writeAttr("lineOrder", "lineOrder", []byte{0})
	buf.WriteByte(0)

	offsetTableStart := buf.Len()
	offsets := make([]int64, height)
	for range offsets {
		binary.Write(&buf, binary.LittleEndian, int64(0))
	}

	for y := 0; y < height; y++ {
		offsets[y] = int64(buf.Len())
		binary.Write(&buf, binary.LittleEndian, int32(y))

		var pixelData bytes.Buffer
		for x := 0; x < width; x++ {
			binary.Write(&pixelData, binary.LittleEndian, floatToHalf(gVals[y*width+x]))
		}
		for x := 0; x < width; x++ {
			binary.Write(&pixelData, binary.LittleEndian, floatToHalf(rVals[y*width+x]))
		}

		rawPixels := pixelData.Bytes()
		binary.Write(&buf, binary.LittleEndian, int32(len(rawPixels)))
		buf.Write(rawPixels)
	}

	data := buf.Bytes()
	for i, off := range offsets {
		binary.LittleEndian.PutUint64(data[offsetTableStart+i*8:], uint64(off))
	}
	return data
}

// floatToHalf converts float32 to IEEE 754 half-float (for test data generation).
func floatToHalf(f float32) uint16 {
	bits := math.Float32bits(f)
	sign := uint16((bits >> 16) & 0x8000)
	exp := int((bits>>23)&0xFF) - 127
	mant := bits & 0x7FFFFF

	if exp > 15 {
		// Overflow to infinity
		return sign | 0x7C00
	}
	if exp < -14 {
		if exp < -24 {
			return sign // underflow to zero
		}
		// Denormalized
		mant |= 0x800000 // add implicit leading 1
		shift := uint(-14 - exp)
		mant >>= (shift + 13) // shift to fit in 10 bits
		return sign | uint16(mant)
	}
	return sign | uint16(exp+15)<<10 | uint16(mant>>13)
}

// buildSyntheticEXRSingleChannel builds an EXR with only one named channel.
func buildSyntheticEXRSingleChannel(t *testing.T, width, height int, channelName string) []byte {
	t.Helper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(0x762F3101))
	binary.Write(&buf, binary.LittleEndian, uint32(2))

	writeAttr := func(name, typeName string, data []byte) {
		buf.WriteString(name)
		buf.WriteByte(0)
		buf.WriteString(typeName)
		buf.WriteByte(0)
		binary.Write(&buf, binary.LittleEndian, int32(len(data)))
		buf.Write(data)
	}

	var chData bytes.Buffer
	chData.WriteString(channelName)
	chData.WriteByte(0)
	binary.Write(&chData, binary.LittleEndian, int32(2)) // FLOAT
	chData.WriteByte(0)
	chData.Write([]byte{0, 0, 0})
	binary.Write(&chData, binary.LittleEndian, int32(1))
	binary.Write(&chData, binary.LittleEndian, int32(1))
	chData.WriteByte(0)
	writeAttr("channels", "chlist", chData.Bytes())

	writeAttr("compression", "compression", []byte{0})

	var dwData bytes.Buffer
	binary.Write(&dwData, binary.LittleEndian, int32(0))
	binary.Write(&dwData, binary.LittleEndian, int32(0))
	binary.Write(&dwData, binary.LittleEndian, int32(width-1))
	binary.Write(&dwData, binary.LittleEndian, int32(height-1))
	writeAttr("dataWindow", "box2i", dwData.Bytes())
	writeAttr("displayWindow", "box2i", dwData.Bytes())
	writeAttr("lineOrder", "lineOrder", []byte{0})
	buf.WriteByte(0)

	// Offset table
	offsetTableStart := buf.Len()
	offsets := make([]int64, height)
	for range offsets {
		binary.Write(&buf, binary.LittleEndian, int64(0))
	}

	for y := 0; y < height; y++ {
		offsets[y] = int64(buf.Len())
		binary.Write(&buf, binary.LittleEndian, int32(y))
		var pixelData bytes.Buffer
		for x := 0; x < width; x++ {
			binary.Write(&pixelData, binary.LittleEndian, float32(0))
		}
		rawPixels := pixelData.Bytes()
		binary.Write(&buf, binary.LittleEndian, int32(len(rawPixels)))
		buf.Write(rawPixels)
	}

	data := buf.Bytes()
	for i, off := range offsets {
		binary.LittleEndian.PutUint64(data[offsetTableStart+i*8:], uint64(off))
	}
	return data
}

// buildSyntheticEXRWithCompression builds a minimal EXR header with
// the specified compression type but no valid scanline data (used to
// test unsupported compression rejection).
func buildSyntheticEXRWithCompression(t *testing.T, width, height int, compression byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(0x762F3101))
	binary.Write(&buf, binary.LittleEndian, uint32(2))

	writeAttr := func(name, typeName string, data []byte) {
		buf.WriteString(name)
		buf.WriteByte(0)
		buf.WriteString(typeName)
		buf.WriteByte(0)
		binary.Write(&buf, binary.LittleEndian, int32(len(data)))
		buf.Write(data)
	}

	var chData bytes.Buffer
	chData.WriteString("G")
	chData.WriteByte(0)
	binary.Write(&chData, binary.LittleEndian, int32(2))
	chData.WriteByte(0)
	chData.Write([]byte{0, 0, 0})
	binary.Write(&chData, binary.LittleEndian, int32(1))
	binary.Write(&chData, binary.LittleEndian, int32(1))
	chData.WriteString("R")
	chData.WriteByte(0)
	binary.Write(&chData, binary.LittleEndian, int32(2))
	chData.WriteByte(0)
	chData.Write([]byte{0, 0, 0})
	binary.Write(&chData, binary.LittleEndian, int32(1))
	binary.Write(&chData, binary.LittleEndian, int32(1))
	chData.WriteByte(0)
	writeAttr("channels", "chlist", chData.Bytes())

	writeAttr("compression", "compression", []byte{compression})

	var dwData bytes.Buffer
	binary.Write(&dwData, binary.LittleEndian, int32(0))
	binary.Write(&dwData, binary.LittleEndian, int32(0))
	binary.Write(&dwData, binary.LittleEndian, int32(width-1))
	binary.Write(&dwData, binary.LittleEndian, int32(height-1))
	writeAttr("dataWindow", "box2i", dwData.Bytes())
	writeAttr("displayWindow", "box2i", dwData.Bytes())
	writeAttr("lineOrder", "lineOrder", []byte{0})
	buf.WriteByte(0)

	return buf.Bytes()
}
