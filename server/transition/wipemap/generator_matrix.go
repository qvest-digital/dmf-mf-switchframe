package wipemap

import (
	"math"
	"math/rand"
)

// GenVenetianBlind creates repeating ramp stripes.
// count is the number of blinds.
// horizontal means the blinds are horizontal (ramp along y); false means vertical (ramp along x).
func GenVenetianBlind(count int, horizontal bool) GeneratorFunc {
	if count < 1 {
		count = 1
	}
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		if horizontal {
			for y := 0; y < h; y++ {
				frac := math.Mod(float64(y)/float64(h)*float64(count), 1.0)
				v := clampByte(frac * 255.0)
				row := buf[y*w : y*w+w]
				for i := range row {
					row[i] = v
				}
			}
		} else {
			for x := 0; x < w; x++ {
				frac := math.Mod(float64(x)/float64(w)*float64(count), 1.0)
				v := clampByte(frac * 255.0)
				for y := 0; y < h; y++ {
					buf[y*w+x] = v
				}
			}
		}
		return buf
	}
}

// GenCheckerboard creates a grid of blocks where each block gets a gradient value
// based on its position in a serpentine (boustrophedon) pattern through the grid.
func GenCheckerboard(cols, rows int) GeneratorFunc {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		totalBlocks := cols * rows
		blockW := w / cols
		blockH := h / rows
		if blockW < 1 {
			blockW = 1
		}
		if blockH < 1 {
			blockH = 1
		}

		for y := 0; y < h; y++ {
			row := y / blockH
			if row >= rows {
				row = rows - 1
			}
			rowOff := y * w
			for x := 0; x < w; x++ {
				col := x / blockW
				if col >= cols {
					col = cols - 1
				}
				// Serpentine: even rows go left-to-right, odd rows go right-to-left.
				var blockIdx int
				if row%2 == 0 {
					blockIdx = row*cols + col
				} else {
					blockIdx = row*cols + (cols - 1 - col)
				}
				v := float64(blockIdx) / float64(max(totalBlocks-1, 1)) * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

// GenCheckerboardDiagonal creates a 45-degree rotated checkerboard pattern.
// size is the side length of each diamond cell in pixels.
func GenCheckerboardDiagonal(size int) GeneratorFunc {
	if size < 1 {
		size = 1
	}
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		fSize := float64(size)

		for y := 0; y < h; y++ {
			fy := float64(y)
			rowOff := y * w
			for x := 0; x < w; x++ {
				fx := float64(x)
				// Rotated coordinates
				u := (fx + fy) / fSize
				v := (fx - fy) / fSize
				// Cell indices
				cu := int(math.Floor(u))
				cv := int(math.Floor(v))
				// Checkerboard parity
				parity := (cu + cv) % 2
				if parity < 0 {
					parity = -parity
				}
				// Assign gradient value based on cell position (serpentine through diagonal)
				cellVal := float64(cu+cv) / float64(max(w+h, 1)/size+1)
				cellVal = math.Mod(math.Abs(cellVal), 1.0)
				// Alternate parity shifts value
				val := cellVal
				if parity == 1 {
					val = 1.0 - cellVal
				}
				buf[rowOff+x] = clampByte(val * 255.0)
			}
		}
		return buf
	}
}

// GenRandom produces a deterministic random gradient map.
// blockSize controls granularity: 1 = per-pixel, N = NxN blocks.
// seed provides deterministic randomness.
func GenRandom(blockSize int, seed int64) GeneratorFunc {
	if blockSize < 1 {
		blockSize = 1
	}
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		rng := rand.New(rand.NewSource(seed))

		blocksX := (w + blockSize - 1) / blockSize
		blocksY := (h + blockSize - 1) / blockSize
		values := make([]byte, blocksX*blocksY)
		for i := range values {
			values[i] = byte(rng.Intn(256))
		}

		for y := 0; y < h; y++ {
			by := y / blockSize
			rowOff := y * w
			for x := 0; x < w; x++ {
				bx := x / blockSize
				buf[rowOff+x] = values[by*blocksX+bx]
			}
		}
		return buf
	}
}

// GenMatrixCascade creates a "Matrix"-style cascade where columns fall at different
// random speeds. Each column is a vertical ramp with a random offset.
func GenMatrixCascade() GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		rng := rand.New(rand.NewSource(12345))

		offsets := make([]float64, w)
		for x := 0; x < w; x++ {
			offsets[x] = rng.Float64() * 0.5 // 0-50% offset
		}

		for y := 0; y < h; y++ {
			yNorm := float64(y) / float64(max(h-1, 1))
			rowOff := y * w
			for x := 0; x < w; x++ {
				v := (yNorm + offsets[x])
				if v > 1.0 {
					v = 1.0
				}
				buf[rowOff+x] = clampByte(v * 255.0)
			}
		}
		return buf
	}
}
