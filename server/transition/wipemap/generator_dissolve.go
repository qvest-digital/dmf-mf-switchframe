package wipemap

import (
	"math"
	"math/rand"
)

// GenDissolve creates various dissolve-style patterns.
// style: "perlin" (smooth noise), "blocks" (random blocks), "scanline" (line-by-line
// with random offset), "burn" (edges first via inverted distance from center).
func GenDissolve(style string) GeneratorFunc {
	switch style {
	case "perlin":
		return genDissolvePerlin
	case "blocks":
		return genDissolveBlocks
	case "scanline":
		return genDissolveScanline
	case "burn":
		return genDissolveBurn
	default:
		return genDissolvePerlin
	}
}

func genDissolvePerlin(w, h int) []byte {
	buf := make([]byte, w*h)
	// Use value noise with multiple octaves for a smooth dissolve pattern.
	// Grid scale: how many noise cells span the image.
	const gridScale = 8.0

	for y := 0; y < h; y++ {
		fy := float64(y) / float64(max(h-1, 1)) * gridScale
		rowOff := y * w
		for x := 0; x < w; x++ {
			fx := float64(x) / float64(max(w-1, 1)) * gridScale
			// Multi-octave value noise.
			n := valueNoise(fx, fy)*0.5 + valueNoise(fx*2, fy*2)*0.3 + valueNoise(fx*4, fy*4)*0.2
			buf[rowOff+x] = clampByte(n * 255.0)
		}
	}
	return buf
}

// valueNoise produces a smooth noise value in [0,1] using bilinear interpolation
// of random grid values.
func valueNoise(x, y float64) float64 {
	xi := int(math.Floor(x))
	yi := int(math.Floor(y))
	xf := x - float64(xi)
	yf := y - float64(yi)

	// Smoothstep for interpolation.
	u := xf * xf * (3 - 2*xf)
	v := yf * yf * (3 - 2*yf)

	// Hash-based pseudo-random at grid points.
	n00 := gridRandom(xi, yi)
	n10 := gridRandom(xi+1, yi)
	n01 := gridRandom(xi, yi+1)
	n11 := gridRandom(xi+1, yi+1)

	// Bilinear interpolation.
	nx0 := n00*(1-u) + n10*u
	nx1 := n01*(1-u) + n11*u
	return nx0*(1-v) + nx1*v
}

// gridRandom produces a deterministic pseudo-random value in [0,1] for grid coords.
func gridRandom(x, y int) float64 {
	// Simple hash: based on well-known integer hash.
	h := x*374761393 + y*668265263
	h = (h ^ (h >> 13)) * 1274126177
	h = h ^ (h >> 16)
	return float64(uint32(h)%65536) / 65535.0
}

func genDissolveBlocks(w, h int) []byte {
	const blockSize = 8
	return GenRandom(blockSize, 7919)(w, h) // deterministic prime seed
}

func genDissolveScanline(w, h int) []byte {
	buf := make([]byte, w*h)
	rng := rand.New(rand.NewSource(31337))

	// Each scanline gets a base value (top-to-bottom gradient) plus random jitter.
	for y := 0; y < h; y++ {
		base := float64(y) / float64(max(h-1, 1)) * 200.0 // 0-200 base
		jitter := rng.Float64() * 55.0                     // 0-55 jitter
		v := clampByte(base + jitter)
		row := buf[y*w : y*w+w]
		for i := range row {
			row[i] = v
		}
	}
	return buf
}

func genDissolveBurn(w, h int) []byte {
	buf := make([]byte, w*h)
	cx := float64(w) / 2.0
	cy := float64(h) / 2.0
	maxR := math.Sqrt(cx*cx + cy*cy)
	if maxR < 1 {
		maxR = 1
	}

	for y := 0; y < h; y++ {
		dy := float64(y) - cy
		rowOff := y * w
		for x := 0; x < w; x++ {
			dx := float64(x) - cx
			r := math.Sqrt(dx*dx + dy*dy)
			// Invert: edges first (low value), center last (high value).
			v := (1.0 - r/maxR) * 255.0
			buf[rowOff+x] = clampByte(v)
		}
	}
	return buf
}
