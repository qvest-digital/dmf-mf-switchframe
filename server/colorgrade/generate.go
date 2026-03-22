package colorgrade

import (
	"fmt"
	"io"
	"math"
)

// GenerateIdentityLUT creates a neutral 33x33x33 LUT (passthrough).
func GenerateIdentityLUT() *LUT3D {
	const size = 33
	total := size * size * size * 3
	data := make([]float32, total)

	idx := 0
	for b := 0; b < size; b++ {
		for g := 0; g < size; g++ {
			for r := 0; r < size; r++ {
				data[idx] = float32(r) / float32(size-1)
				data[idx+1] = float32(g) / float32(size-1)
				data[idx+2] = float32(b) / float32(size-1)
				idx += 3
			}
		}
	}

	return &LUT3D{
		Size: size,
		Data: data,
	}
}

// WriteCube writes a LUT3D to .cube format.
func WriteCube(w io.Writer, lut *LUT3D) error {
	if lut.Title != "" {
		if _, err := fmt.Fprintf(w, "TITLE \"%s\"\n", lut.Title); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "LUT_3D_SIZE %d\n", lut.Size); err != nil {
		return err
	}

	for i := 0; i < len(lut.Data); i += 3 {
		if _, err := fmt.Fprintf(w, "%.6f %.6f %.6f\n",
			lut.Data[i], lut.Data[i+1], lut.Data[i+2]); err != nil {
			return err
		}
	}
	return nil
}

// ApplyCDL applies ASC-CDL (slope, offset, power) per channel to a LUT.
// Formula: out = clamp(pow(clamp(in * slope + offset, 0, 1), power), 0, 1)
func ApplyCDL(lut *LUT3D, slopeR, slopeG, slopeB, offR, offG, offB, powR, powG, powB float32) {
	for i := 0; i < len(lut.Data); i += 3 {
		r := lut.Data[i]*slopeR + offR
		g := lut.Data[i+1]*slopeG + offG
		b := lut.Data[i+2]*slopeB + offB

		r = clampf(r, 0, 1)
		g = clampf(g, 0, 1)
		b = clampf(b, 0, 1)

		lut.Data[i] = clampf(powf(r, powR), 0, 1)
		lut.Data[i+1] = clampf(powf(g, powG), 0, 1)
		lut.Data[i+2] = clampf(powf(b, powB), 0, 1)
	}
}

// ApplySaturation adjusts color saturation (1.0 = neutral, 0.0 = grayscale, >1.0 = boosted).
// Uses BT.709 luminance coefficients.
func ApplySaturation(lut *LUT3D, sat float32) {
	for i := 0; i < len(lut.Data); i += 3 {
		r := lut.Data[i]
		g := lut.Data[i+1]
		b := lut.Data[i+2]

		// BT.709 luminance
		lum := 0.2126*r + 0.7152*g + 0.0722*b

		lut.Data[i] = clampf(lum+(r-lum)*sat, 0, 1)
		lut.Data[i+1] = clampf(lum+(g-lum)*sat, 0, 1)
		lut.Data[i+2] = clampf(lum+(b-lum)*sat, 0, 1)
	}
}

// ApplyContrast applies an S-curve contrast around the midpoint (0.5).
// amount controls strength: 0.0 = no change, positive = more contrast.
func ApplyContrast(lut *LUT3D, amount float32) {
	for i := 0; i < len(lut.Data); i++ {
		v := lut.Data[i]
		// Attempt a smooth S-curve via adjusted sigmoid.
		// Map [0,1] through: 0.5 + 0.5 * tanh(amount * (v - 0.5) * 4) / tanh(amount * 2)
		if amount > 0 {
			t := float64(amount) * 2.0
			norm := math.Tanh(t)
			v = float32(0.5 + 0.5*math.Tanh(float64(amount)*float64(v-0.5)*4.0)/norm)
		}
		lut.Data[i] = clampf(v, 0, 1)
	}
}

func clampf(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func powf(base, exp float32) float32 {
	return float32(math.Pow(float64(base), float64(exp)))
}

// LookParams defines the parameters for generating a color look.
type LookParams struct {
	Name                           string
	SlopeR, SlopeG, SlopeB         float32
	OffsetR, OffsetG, OffsetB      float32
	PowerR, PowerG, PowerB         float32
	Saturation                     float32
	Contrast                       float32
}

// BuiltinLooks returns the parameters for all 6 built-in broadcast looks.
func BuiltinLooks() []LookParams {
	return []LookParams{
		{
			Name:       "broadcast-news",
			SlopeR:     1.05, SlopeG: 1.02, SlopeB: 1.08,
			OffsetR:    0, OffsetG: 0, OffsetB: 0.01,
			PowerR:     0.95, PowerG: 1.0, PowerB: 0.95,
			Saturation: 0.95,
			Contrast:   0.15,
		},
		{
			Name:       "cinematic",
			SlopeR:     1.1, SlopeG: 0.95, SlopeB: 0.85,
			OffsetR:    0.02, OffsetG: 0, OffsetB: -0.01,
			PowerR:     0.9, PowerG: 1.0, PowerB: 1.1,
			Saturation: 0.85,
			Contrast:   0.2,
		},
		{
			Name:       "warm-natural",
			SlopeR:     1.08, SlopeG: 1.02, SlopeB: 0.92,
			OffsetR:    0.01, OffsetG: 0.005, OffsetB: -0.01,
			PowerR:     0.95, PowerG: 0.98, PowerB: 1.05,
			Saturation: 1.05,
			Contrast:   0.1,
		},
		{
			Name:       "cool-drama",
			SlopeR:     0.95, SlopeG: 0.98, SlopeB: 1.1,
			OffsetR:    -0.01, OffsetG: 0, OffsetB: 0.02,
			PowerR:     1.05, PowerG: 1.0, PowerB: 0.92,
			Saturation: 0.8,
			Contrast:   0.25,
		},
		{
			Name:       "vivid",
			SlopeR:     1.1, SlopeG: 1.1, SlopeB: 1.1,
			OffsetR:    0, OffsetG: 0, OffsetB: 0,
			PowerR:     0.9, PowerG: 0.9, PowerB: 0.9,
			Saturation: 1.3,
			Contrast:   0.2,
		},
		{
			Name:       "log-to-rec709",
			SlopeR:     1.5, SlopeG: 1.5, SlopeB: 1.5,
			OffsetR:    0.05, OffsetG: 0.05, OffsetB: 0.05,
			PowerR:     0.45, PowerG: 0.45, PowerB: 0.45,
			Saturation: 1.1,
			Contrast:   0.1,
		},
	}
}

// GenerateLook creates a 33x33x33 LUT with the given look parameters applied.
func GenerateLook(params LookParams) *LUT3D {
	lut := GenerateIdentityLUT()
	lut.Title = params.Name

	ApplyCDL(lut,
		params.SlopeR, params.SlopeG, params.SlopeB,
		params.OffsetR, params.OffsetG, params.OffsetB,
		params.PowerR, params.PowerG, params.PowerB,
	)
	ApplySaturation(lut, params.Saturation)
	ApplyContrast(lut, params.Contrast)

	return lut
}
