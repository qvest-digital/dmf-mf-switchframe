package stmap

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// EXR format constants.
const (
	exrMagic = 0x762F3101

	// Version flags (bits 9-11).
	exrTiledFlag    = 0x200
	exrLongNameFlag = 0x400
	exrDeepFlag     = 0x800
	exrMultiPartFlag = 0x1000

	// Compression types.
	exrNoCompression   = 0
	exrRLECompression  = 1
	exrZIPSCompression = 2
	exrZIPCompression  = 3

	// Channel pixel types.
	exrPixelTypeUINT  = 0
	exrPixelTypeHALF  = 1
	exrPixelTypeFLOAT = 2
)

// exrChannel describes a single channel in the EXR channel list.
type exrChannel struct {
	name      string
	pixelType int32
	xSampling int32
	ySampling int32
}

// pixelSize returns the byte size of a single pixel value for this channel.
func (c *exrChannel) pixelSize() int {
	switch c.pixelType {
	case exrPixelTypeHALF:
		return 2
	case exrPixelTypeFLOAT, exrPixelTypeUINT:
		return 4
	default:
		return 0
	}
}

// ReadEXR parses an OpenEXR file from a byte slice and extracts the R and G
// channels as an STMap. Only scanline format is supported (not tiled or deep).
// Supported compression: none, ZIPS (per-scanline zlib), ZIP (per-16-scanline zlib).
// Supported pixel types: UINT, HALF, FLOAT.
func ReadEXR(data []byte, name string) (*STMap, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("exr: data too short for magic number")
	}

	// Check magic number.
	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != exrMagic {
		return nil, fmt.Errorf("exr: invalid magic number 0x%08X (expected 0x%08X)", magic, exrMagic)
	}

	// Check version and flags.
	version := binary.LittleEndian.Uint32(data[4:8])
	if version&exrTiledFlag != 0 {
		return nil, fmt.Errorf("exr: tiled images are not supported")
	}
	if version&exrDeepFlag != 0 {
		return nil, fmt.Errorf("exr: deep data images are not supported")
	}
	if version&exrMultiPartFlag != 0 {
		return nil, fmt.Errorf("exr: multi-part images are not supported")
	}

	// Parse header attributes.
	pos := 8
	var channels []exrChannel
	compression := int32(-1)
	var dataWindow [4]int32 // xMin, yMin, xMax, yMax

	haveChannels := false
	haveCompression := false
	haveDataWindow := false

	for {
		if pos >= len(data) {
			return nil, fmt.Errorf("exr: unexpected end of header")
		}

		// Read attribute name (null-terminated).
		attrName, n, err := readNullTermString(data, pos)
		if err != nil {
			return nil, fmt.Errorf("exr: reading attribute name: %w", err)
		}
		pos += n

		// Empty name signals end of header.
		if attrName == "" {
			break
		}

		// Read attribute type name (null-terminated).
		_, tn, err := readNullTermString(data, pos)
		if err != nil {
			return nil, fmt.Errorf("exr: reading attribute type for %q: %w", attrName, err)
		}
		pos += tn

		// Read attribute data size.
		if pos+4 > len(data) {
			return nil, fmt.Errorf("exr: truncated attribute size for %q", attrName)
		}
		attrSize := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4

		if pos+attrSize > len(data) {
			return nil, fmt.Errorf("exr: truncated attribute data for %q", attrName)
		}
		attrData := data[pos : pos+attrSize]
		pos += attrSize

		switch attrName {
		case "channels":
			channels, err = parseChannelList(attrData)
			if err != nil {
				return nil, fmt.Errorf("exr: parsing channel list: %w", err)
			}
			haveChannels = true

		case "compression":
			if len(attrData) < 1 {
				return nil, fmt.Errorf("exr: empty compression attribute")
			}
			compression = int32(attrData[0])
			haveCompression = true

		case "dataWindow":
			if len(attrData) < 16 {
				return nil, fmt.Errorf("exr: dataWindow attribute too short")
			}
			dataWindow[0] = int32(binary.LittleEndian.Uint32(attrData[0:4]))
			dataWindow[1] = int32(binary.LittleEndian.Uint32(attrData[4:8]))
			dataWindow[2] = int32(binary.LittleEndian.Uint32(attrData[8:12]))
			dataWindow[3] = int32(binary.LittleEndian.Uint32(attrData[12:16]))
			haveDataWindow = true
		}
	}

	if !haveChannels {
		return nil, fmt.Errorf("exr: missing channels attribute")
	}
	if !haveCompression {
		return nil, fmt.Errorf("exr: missing compression attribute")
	}
	if !haveDataWindow {
		return nil, fmt.Errorf("exr: missing dataWindow attribute")
	}

	// Validate compression.
	switch compression {
	case exrNoCompression, exrZIPSCompression, exrZIPCompression:
		// OK
	default:
		return nil, fmt.Errorf("exr: unsupported compression type %d (supported: none=0, ZIPS=2, ZIP=3)", compression)
	}

	// Compute dimensions with overflow-safe arithmetic.
	width := int(dataWindow[2]-dataWindow[0]) + 1
	height := int(dataWindow[3]-dataWindow[1]) + 1

	if width <= 0 || height <= 0 || width%2 != 0 || height%2 != 0 {
		return nil, ErrInvalidDimensions
	}
	// Reject unreasonably large dimensions to prevent OOM from adversarial files.
	if width > 16384 || height > 16384 {
		return nil, fmt.Errorf("exr: dimensions %dx%d exceed maximum 16384x16384", width, height)
	}

	// Find R and G channels, record their indices in the alphabetically-sorted channel order.
	rIdx := -1
	gIdx := -1
	for i, ch := range channels {
		switch ch.name {
		case "R":
			rIdx = i
		case "G":
			gIdx = i
		}
	}
	if rIdx < 0 || gIdx < 0 {
		return nil, fmt.Errorf("exr: missing required channels (need R and G, found: %s)", channelNames(channels))
	}

	// Validate channel sampling (must be 1:1).
	for _, idx := range []int{rIdx, gIdx} {
		ch := channels[idx]
		if ch.xSampling != 1 || ch.ySampling != 1 {
			return nil, fmt.Errorf("exr: channel %q has non-unity sampling (%d, %d)", ch.name, ch.xSampling, ch.ySampling)
		}
	}

	// Determine scanline block size.
	rowsPerBlock := 1
	if compression == exrZIPCompression {
		rowsPerBlock = 16
	}

	numBlocks := (height + rowsPerBlock - 1) / rowsPerBlock

	// Read offset table.
	if pos+numBlocks*8 > len(data) {
		return nil, fmt.Errorf("exr: truncated offset table")
	}
	offsets := make([]int64, numBlocks)
	for i := range offsets {
		offsets[i] = int64(binary.LittleEndian.Uint64(data[pos : pos+8]))
		pos += 8
	}

	// Allocate output.
	n := width * height
	sData := make([]float32, n)
	tData := make([]float32, n)

	yMin := int(dataWindow[1])

	// Read scanline blocks.
	for block := 0; block < numBlocks; block++ {
		blockOffset := int(offsets[block])
		if blockOffset+8 > len(data) {
			return nil, fmt.Errorf("exr: scanline block %d offset out of range", block)
		}

		// y-coordinate of first scanline in block.
		blockY := int(int32(binary.LittleEndian.Uint32(data[blockOffset : blockOffset+4])))
		pixelDataSize := int(binary.LittleEndian.Uint32(data[blockOffset+4 : blockOffset+8]))

		if blockOffset+8+pixelDataSize > len(data) {
			return nil, fmt.Errorf("exr: scanline block %d data extends beyond file", block)
		}

		pixelBytes := data[blockOffset+8 : blockOffset+8+pixelDataSize]

		// Decompress if needed.
		if compression == exrZIPSCompression || compression == exrZIPCompression {
			decompressed, err := zlibDecompress(pixelBytes)
			if err != nil {
				return nil, fmt.Errorf("exr: decompressing scanline block %d: %w", block, err)
			}
			pixelBytes = decompressed
		}

		// Determine rows in this block.
		startRow := blockY - yMin
		endRow := startRow + rowsPerBlock
		if endRow > height {
			endRow = height
		}
		rowsInBlock := endRow - startRow

		// EXR block layout: channels are stored sequentially within each block.
		// For each channel (in alphabetical order), all rows of that channel
		// appear contiguously: [ch0_row0, ch0_row1, ..., ch1_row0, ch1_row1, ...].
		// Compute byte offset of each channel's data within the block.
		channelBlockOffset := make([]int, len(channels))
		channelRowBytes := make([]int, len(channels))
		blockByteOffset := 0
		for i, ch := range channels {
			channelBlockOffset[i] = blockByteOffset
			channelRowBytes[i] = ch.pixelSize() * width
			blockByteOffset += channelRowBytes[i] * rowsInBlock
		}

		if len(pixelBytes) < blockByteOffset {
			return nil, fmt.Errorf("exr: scanline block %d: expected %d bytes of pixel data, got %d",
				block, blockByteOffset, len(pixelBytes))
		}

		// Extract R and G channels.
		rPS := channels[rIdx].pixelSize()
		rBlockOff := channelBlockOffset[rIdx]
		rRowBytes := channelRowBytes[rIdx]

		gPS := channels[gIdx].pixelSize()
		gBlockOff := channelBlockOffset[gIdx]
		gRowBytes := channelRowBytes[gIdx]

		for row := 0; row < rowsInBlock; row++ {
			absY := startRow + row

			rRowOff := rBlockOff + row*rRowBytes
			for x := 0; x < width; x++ {
				sData[absY*width+x] = readPixel(pixelBytes, rRowOff+x*rPS, channels[rIdx].pixelType)
			}

			gRowOff := gBlockOff + row*gRowBytes
			for x := 0; x < width; x++ {
				tData[absY*width+x] = readPixel(pixelBytes, gRowOff+x*gPS, channels[gIdx].pixelType)
			}
		}
	}

	return &STMap{
		Name:   name,
		Width:  width,
		Height: height,
		S:      sData,
		T:      tData,
	}, nil
}

// readNullTermString reads a null-terminated string starting at data[pos].
// Returns the string, the number of bytes consumed (including the null), and any error.
func readNullTermString(data []byte, pos int) (string, int, error) {
	end := bytes.IndexByte(data[pos:], 0)
	if end < 0 {
		return "", 0, fmt.Errorf("unterminated string at offset %d", pos)
	}
	return string(data[pos : pos+end]), end + 1, nil
}

// parseChannelList parses an EXR channel list attribute value.
// The format is: repeated (name\0, pixelType int32, pLinear uint8, reserved[3],
// xSampling int32, ySampling int32), terminated by a single \0 byte.
func parseChannelList(data []byte) ([]exrChannel, error) {
	var channels []exrChannel
	pos := 0

	for pos < len(data) {
		// Check for terminating null.
		if data[pos] == 0 {
			break
		}

		// Read channel name.
		name, n, err := readNullTermString(data, pos)
		if err != nil {
			return nil, fmt.Errorf("reading channel name: %w", err)
		}
		pos += n

		// Need 16 more bytes: pixelType(4) + pLinear(1) + reserved(3) + xSampling(4) + ySampling(4).
		if pos+16 > len(data) {
			return nil, fmt.Errorf("truncated channel entry for %q", name)
		}

		pixelType := int32(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4
		pos += 4 // skip pLinear(1) + reserved(3)

		xSampling := int32(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4
		ySampling := int32(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4

		if pixelType < 0 || pixelType > 2 {
			return nil, fmt.Errorf("unsupported pixel type %d for channel %q", pixelType, name)
		}

		channels = append(channels, exrChannel{
			name:      name,
			pixelType: pixelType,
			xSampling: xSampling,
			ySampling: ySampling,
		})
	}

	return channels, nil
}

// readPixel reads a single pixel value at the given byte offset for the given pixel type.
func readPixel(data []byte, off int, pixelType int32) float32 {
	switch pixelType {
	case exrPixelTypeHALF:
		h := binary.LittleEndian.Uint16(data[off : off+2])
		return halfToFloat(h)
	case exrPixelTypeFLOAT:
		return math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4]))
	case exrPixelTypeUINT:
		u := binary.LittleEndian.Uint32(data[off : off+4])
		return float32(u) / float32(math.MaxUint32)
	default:
		return 0
	}
}

// halfToFloat converts an IEEE 754 half-precision (16-bit) float to float32.
func halfToFloat(h uint16) float32 {
	sign := uint32(h>>15) & 1
	exp := uint32(h>>10) & 0x1F
	mant := uint32(h) & 0x3FF

	if exp == 0 {
		if mant == 0 {
			return math.Float32frombits(sign << 31)
		}
		// Denormalized: normalize by shifting mantissa up.
		for mant&0x400 == 0 {
			mant <<= 1
			exp--
		}
		exp++
		mant &= 0x3FF
		return math.Float32frombits((sign << 31) | ((exp + 112) << 23) | (mant << 13))
	}
	if exp == 31 {
		if mant == 0 {
			return math.Float32frombits((sign << 31) | 0x7F800000) // Inf
		}
		return math.Float32frombits((sign << 31) | 0x7F800000 | (mant << 13)) // NaN
	}
	return math.Float32frombits((sign << 31) | ((exp + 112) << 23) | (mant << 13))
}

// maxDecompressedSize is the upper bound for zlib decompression to prevent
// zip bomb attacks. 100MB is generous for any valid ST map scanline block
// (a 4K ZIP block is ~491KB uncompressed).
const maxDecompressedSize = 100 << 20

// zlibDecompress decompresses zlib-compressed data with a size limit.
func zlibDecompress(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()

	return io.ReadAll(io.LimitReader(r, maxDecompressedSize))
}

// channelNames returns a comma-separated list of channel names for error messages.
func channelNames(channels []exrChannel) string {
	if len(channels) == 0 {
		return "(none)"
	}
	var b bytes.Buffer
	for i, ch := range channels {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(ch.name)
	}
	return b.String()
}

// IsEXR checks if data begins with the OpenEXR magic number.
func IsEXR(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return binary.LittleEndian.Uint32(data[0:4]) == exrMagic
}
