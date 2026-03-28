package switcher

import "encoding/binary"

// SAD10 computes the Sum of Absolute Differences between two blocks in a
// YUV422P10LE Y plane. The block dimensions are bw x bh samples, and stride
// is in uint16 samples (not bytes). aOff and bOff are byte offsets into a and b.
func SAD10(a, b []byte, aOff, bOff, stride, bw, bh int) int {
	sum := 0
	for y := 0; y < bh; y++ {
		for x := 0; x < bw; x++ {
			aVal := int(binary.LittleEndian.Uint16(a[aOff+x*2:]))
			bVal := int(binary.LittleEndian.Uint16(b[bOff+x*2:]))
			diff := aVal - bVal
			if diff < 0 {
				diff = -diff
			}
			sum += diff
		}
		aOff += stride * 2
		bOff += stride * 2
	}
	return sum
}

// detectSceneChange10 detects scene changes between two YUV422P10LE frames
// by computing subsampled SAD on the Y plane. Returns true if the average
// per-pixel SAD exceeds the threshold (scaled to 10-bit range).
func detectSceneChange10(frameA, frameB []byte, width, height int) bool {
	ySize := width * height * 2 // 10-bit: 2 bytes per sample
	if len(frameA) < ySize || len(frameB) < ySize {
		return true
	}

	var totalSAD uint64
	var pixels int
	for row := 0; row < height; row += 4 {
		for col := 0; col < width; col += 4 {
			off := (row*width + col) * 2
			aVal := int64(binary.LittleEndian.Uint16(frameA[off:]))
			bVal := int64(binary.LittleEndian.Uint16(frameB[off:]))
			d := aVal - bVal
			if d < 0 {
				d = -d
			}
			totalSAD += uint64(d)
			pixels++
		}
	}
	if pixels == 0 {
		return true
	}

	avgSAD := totalSAD / uint64(pixels)
	// Scale 8-bit threshold (25) to 10-bit: 25 * 4 = 100
	return avgSAD > 100
}
