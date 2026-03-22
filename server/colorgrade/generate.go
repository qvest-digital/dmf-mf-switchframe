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
	Group                          string
	Description                    string
	SlopeR, SlopeG, SlopeB         float32
	OffsetR, OffsetG, OffsetB      float32
	PowerR, PowerG, PowerB         float32
	Saturation                     float32
	Contrast                       float32
}

// BuiltinLooks returns the parameters for all 41 built-in color looks
// organized into 7 aesthetic groups.
func BuiltinLooks() []LookParams {
	return []LookParams{
		// ── Light (6) — warm, luminous, golden ──────────────────────────
		{
			Name: "golden-hour", Group: "Light",
			Description: "Amber glow, soft lifted shadows",
			SlopeR: 1.12, SlopeG: 1.02, SlopeB: 0.88,
			OffsetR: 0.03, OffsetG: 0.01, OffsetB: -0.02,
			PowerR: 0.90, PowerG: 0.95, PowerB: 1.08,
			Saturation: 1.10, Contrast: 0.10,
		},
		{
			Name: "candlelight", Group: "Light",
			Description: "Deep intimate warmth, low contrast",
			SlopeR: 1.18, SlopeG: 0.95, SlopeB: 0.80,
			OffsetR: 0.04, OffsetG: 0.02, OffsetB: 0.0,
			PowerR: 0.85, PowerG: 0.95, PowerB: 1.15,
			Saturation: 0.90, Contrast: 0.05,
		},
		{
			Name: "sahara", Group: "Light",
			Description: "Sun-bleached, dusty warmth",
			SlopeR: 1.10, SlopeG: 1.05, SlopeB: 0.90,
			OffsetR: 0.02, OffsetG: 0.01, OffsetB: 0.01,
			PowerR: 0.88, PowerG: 0.92, PowerB: 1.05,
			Saturation: 0.85, Contrast: 0.08,
		},
		{
			Name: "ember", Group: "Light",
			Description: "Smoldering orange, deep blacks",
			SlopeR: 1.20, SlopeG: 0.92, SlopeB: 0.78,
			OffsetR: 0.02, OffsetG: -0.01, OffsetB: -0.03,
			PowerR: 0.88, PowerG: 1.05, PowerB: 1.20,
			Saturation: 1.05, Contrast: 0.22,
		},
		{
			Name: "honey", Group: "Light",
			Description: "Soft golden cast, gentle contrast",
			SlopeR: 1.08, SlopeG: 1.04, SlopeB: 0.92,
			OffsetR: 0.02, OffsetG: 0.01, OffsetB: -0.01,
			PowerR: 0.92, PowerG: 0.96, PowerB: 1.04,
			Saturation: 1.05, Contrast: 0.08,
		},
		{
			Name: "solstice", Group: "Light",
			Description: "Long warm light, extended golden tones",
			SlopeR: 1.14, SlopeG: 1.06, SlopeB: 0.86,
			OffsetR: 0.02, OffsetG: 0.01, OffsetB: -0.01,
			PowerR: 0.88, PowerG: 0.94, PowerB: 1.10,
			Saturation: 1.08, Contrast: 0.12,
		},

		// ── Shadow (6) — cool, atmospheric, moody ──────────────────────
		{
			Name: "glacier", Group: "Shadow",
			Description: "Crisp blue highlights, clean whites",
			SlopeR: 0.92, SlopeG: 0.98, SlopeB: 1.12,
			OffsetR: -0.01, OffsetG: 0.0, OffsetB: 0.02,
			PowerR: 1.04, PowerG: 1.0, PowerB: 0.92,
			Saturation: 0.90, Contrast: 0.15,
		},
		{
			Name: "moonrise", Group: "Shadow",
			Description: "Cool blue wash, soft dreamy contrast",
			SlopeR: 0.90, SlopeG: 0.95, SlopeB: 1.10,
			OffsetR: 0.0, OffsetG: 0.01, OffsetB: 0.03,
			PowerR: 1.05, PowerG: 1.0, PowerB: 0.90,
			Saturation: 0.85, Contrast: 0.08,
		},
		{
			Name: "slate", Group: "Shadow",
			Description: "Desaturated gray, muted and controlled",
			SlopeR: 0.98, SlopeG: 0.98, SlopeB: 1.02,
			OffsetR: 0.0, OffsetG: 0.0, OffsetB: 0.01,
			PowerR: 1.02, PowerG: 1.02, PowerB: 1.0,
			Saturation: 0.55, Contrast: 0.12,
		},
		{
			Name: "cobalt", Group: "Shadow",
			Description: "Deep blue-black shadows, rich",
			SlopeR: 0.88, SlopeG: 0.94, SlopeB: 1.15,
			OffsetR: -0.02, OffsetG: -0.01, OffsetB: 0.02,
			PowerR: 1.10, PowerG: 1.05, PowerB: 0.88,
			Saturation: 0.90, Contrast: 0.20,
		},
		{
			Name: "frost", Group: "Shadow",
			Description: "Pale icy tones, ethereal",
			SlopeR: 0.95, SlopeG: 1.0, SlopeB: 1.08,
			OffsetR: 0.02, OffsetG: 0.02, OffsetB: 0.04,
			PowerR: 0.90, PowerG: 0.92, PowerB: 0.88,
			Saturation: 0.75, Contrast: 0.05,
		},
		{
			Name: "twilight", Group: "Shadow",
			Description: "Purple-blue shift, deep shadows",
			SlopeR: 0.94, SlopeG: 0.90, SlopeB: 1.12,
			OffsetR: 0.01, OffsetG: -0.01, OffsetB: 0.03,
			PowerR: 1.08, PowerG: 1.10, PowerB: 0.90,
			Saturation: 0.88, Contrast: 0.18,
		},

		// ── Film (7) — analog stock emulation ──────────────────────────
		{
			Name: "kodachrome", Group: "Film",
			Description: "Legendary warmth, rich reds, deep blacks",
			SlopeR: 1.12, SlopeG: 1.0, SlopeB: 0.88,
			OffsetR: 0.02, OffsetG: 0.0, OffsetB: -0.02,
			PowerR: 0.90, PowerG: 0.98, PowerB: 1.10,
			Saturation: 1.15, Contrast: 0.18,
		},
		{
			Name: "portra", Group: "Film",
			Description: "Warm skin tones, soft pastels, lifted blacks",
			SlopeR: 1.06, SlopeG: 1.02, SlopeB: 0.96,
			OffsetR: 0.02, OffsetG: 0.01, OffsetB: 0.01,
			PowerR: 0.92, PowerG: 0.95, PowerB: 1.02,
			Saturation: 0.92, Contrast: 0.08,
		},
		{
			Name: "velvia", Group: "Film",
			Description: "Vivid saturated landscape colors, punchy",
			SlopeR: 1.10, SlopeG: 1.08, SlopeB: 1.05,
			OffsetR: 0.0, OffsetG: 0.0, OffsetB: -0.01,
			PowerR: 0.88, PowerG: 0.90, PowerB: 0.95,
			Saturation: 1.35, Contrast: 0.22,
		},
		{
			Name: "tri-x", Group: "Film",
			Description: "Classic black & white, gritty contrast",
			SlopeR: 1.05, SlopeG: 1.05, SlopeB: 1.05,
			OffsetR: 0.0, OffsetG: 0.0, OffsetB: 0.0,
			PowerR: 0.92, PowerG: 0.92, PowerB: 0.92,
			Saturation: 0.0, Contrast: 0.25,
		},
		{
			Name: "ektachrome", Group: "Film",
			Description: "Cool slide film, vivid blues and greens",
			SlopeR: 0.95, SlopeG: 1.06, SlopeB: 1.10,
			OffsetR: -0.01, OffsetG: 0.01, OffsetB: 0.02,
			PowerR: 1.02, PowerG: 0.94, PowerB: 0.92,
			Saturation: 1.18, Contrast: 0.15,
		},
		{
			Name: "superia", Group: "Film",
			Description: "Everyday film, subtle green cast",
			SlopeR: 1.0, SlopeG: 1.04, SlopeB: 0.98,
			OffsetR: 0.0, OffsetG: 0.01, OffsetB: 0.0,
			PowerR: 0.98, PowerG: 0.95, PowerB: 1.02,
			Saturation: 0.95, Contrast: 0.10,
		},
		{
			Name: "cinestill", Group: "Film",
			Description: "Halation warmth, urban night feel",
			SlopeR: 1.14, SlopeG: 0.98, SlopeB: 0.92,
			OffsetR: 0.03, OffsetG: 0.01, OffsetB: 0.01,
			PowerR: 0.88, PowerG: 0.98, PowerB: 1.05,
			Saturation: 1.08, Contrast: 0.15,
		},

		// ── Drama (6) — high impact, cinematic ─────────────────────────
		{
			Name: "noir", Group: "Drama",
			Description: "Deep shadows, high contrast monochrome",
			SlopeR: 1.08, SlopeG: 1.08, SlopeB: 1.08,
			OffsetR: -0.02, OffsetG: -0.02, OffsetB: -0.02,
			PowerR: 1.15, PowerG: 1.15, PowerB: 1.15,
			Saturation: 0.0, Contrast: 0.30,
		},
		{
			Name: "bleach-bypass", Group: "Drama",
			Description: "Desaturated, gritty, raw",
			SlopeR: 1.10, SlopeG: 1.05, SlopeB: 1.0,
			OffsetR: 0.0, OffsetG: 0.0, OffsetB: 0.0,
			PowerR: 0.95, PowerG: 0.98, PowerB: 1.02,
			Saturation: 0.40, Contrast: 0.28,
		},
		{
			Name: "teal-and-orange", Group: "Drama",
			Description: "The blockbuster split tone",
			SlopeR: 1.12, SlopeG: 0.95, SlopeB: 0.85,
			OffsetR: 0.02, OffsetG: 0.0, OffsetB: 0.02,
			PowerR: 0.90, PowerG: 1.02, PowerB: 0.88,
			Saturation: 1.10, Contrast: 0.18,
		},
		{
			Name: "matte", Group: "Drama",
			Description: "Lifted blacks, modern editorial flat",
			SlopeR: 0.95, SlopeG: 0.95, SlopeB: 0.95,
			OffsetR: 0.04, OffsetG: 0.04, OffsetB: 0.04,
			PowerR: 0.92, PowerG: 0.92, PowerB: 0.92,
			Saturation: 0.85, Contrast: 0.05,
		},
		{
			Name: "cross-process", Group: "Drama",
			Description: "Unexpected color shifts, experimental",
			SlopeR: 1.15, SlopeG: 0.92, SlopeB: 1.08,
			OffsetR: 0.02, OffsetG: -0.02, OffsetB: 0.03,
			PowerR: 0.85, PowerG: 1.10, PowerB: 0.90,
			Saturation: 1.15, Contrast: 0.15,
		},
		{
			Name: "anamorphic", Group: "Drama",
			Description: "Warm highlights, cool shadows, wide cinema",
			SlopeR: 1.08, SlopeG: 1.0, SlopeB: 0.92,
			OffsetR: 0.01, OffsetG: 0.0, OffsetB: 0.02,
			PowerR: 0.92, PowerG: 1.0, PowerB: 0.95,
			Saturation: 0.90, Contrast: 0.18,
		},

		// ── Vivid (6) — energetic, saturated, punchy ───────────────────
		{
			Name: "neon", Group: "Vivid",
			Description: "Electric hyper-saturation",
			SlopeR: 1.12, SlopeG: 1.10, SlopeB: 1.15,
			OffsetR: 0.01, OffsetG: 0.0, OffsetB: 0.02,
			PowerR: 0.88, PowerG: 0.88, PowerB: 0.85,
			Saturation: 1.50, Contrast: 0.20,
		},
		{
			Name: "punch", Group: "Vivid",
			Description: "Strong contrast + saturation boost",
			SlopeR: 1.10, SlopeG: 1.10, SlopeB: 1.10,
			OffsetR: 0.0, OffsetG: 0.0, OffsetB: 0.0,
			PowerR: 0.88, PowerG: 0.88, PowerB: 0.88,
			Saturation: 1.30, Contrast: 0.25,
		},
		{
			Name: "prism", Group: "Vivid",
			Description: "Enhanced color separation, rainbow",
			SlopeR: 1.15, SlopeG: 1.08, SlopeB: 1.12,
			OffsetR: 0.01, OffsetG: -0.01, OffsetB: 0.01,
			PowerR: 0.90, PowerG: 0.92, PowerB: 0.88,
			Saturation: 1.40, Contrast: 0.15,
		},
		{
			Name: "surge", Group: "Vivid",
			Description: "Saturated warm push, energetic",
			SlopeR: 1.18, SlopeG: 1.06, SlopeB: 0.95,
			OffsetR: 0.02, OffsetG: 0.01, OffsetB: -0.01,
			PowerR: 0.86, PowerG: 0.92, PowerB: 1.0,
			Saturation: 1.35, Contrast: 0.18,
		},
		{
			Name: "radiant", Group: "Vivid",
			Description: "Bright, clean, vivid",
			SlopeR: 1.08, SlopeG: 1.08, SlopeB: 1.08,
			OffsetR: 0.01, OffsetG: 0.01, OffsetB: 0.01,
			PowerR: 0.85, PowerG: 0.85, PowerB: 0.85,
			Saturation: 1.25, Contrast: 0.12,
		},
		{
			Name: "electric", Group: "Vivid",
			Description: "Punchy blue-teal pop",
			SlopeR: 0.95, SlopeG: 1.08, SlopeB: 1.18,
			OffsetR: -0.01, OffsetG: 0.01, OffsetB: 0.02,
			PowerR: 1.0, PowerG: 0.90, PowerB: 0.85,
			Saturation: 1.30, Contrast: 0.20,
		},

		// ── Vintage (5) — nostalgic, retro, aged ───────────────────────
		{
			Name: "sepia", Group: "Vintage",
			Description: "Classic brown tone",
			SlopeR: 1.10, SlopeG: 0.95, SlopeB: 0.78,
			OffsetR: 0.03, OffsetG: 0.02, OffsetB: 0.0,
			PowerR: 0.90, PowerG: 0.98, PowerB: 1.12,
			Saturation: 0.30, Contrast: 0.10,
		},
		{
			Name: "polaroid", Group: "Vintage",
			Description: "Faded instant film, yellow cast",
			SlopeR: 1.05, SlopeG: 1.02, SlopeB: 0.88,
			OffsetR: 0.03, OffsetG: 0.02, OffsetB: 0.01,
			PowerR: 0.88, PowerG: 0.92, PowerB: 1.05,
			Saturation: 0.80, Contrast: 0.08,
		},
		{
			Name: "faded", Group: "Vintage",
			Description: "Washed-out nostalgia, lifted everything",
			SlopeR: 0.92, SlopeG: 0.92, SlopeB: 0.92,
			OffsetR: 0.05, OffsetG: 0.05, OffsetB: 0.05,
			PowerR: 0.88, PowerG: 0.88, PowerB: 0.88,
			Saturation: 0.70, Contrast: 0.0,
		},
		{
			Name: "technicolor", Group: "Vintage",
			Description: "Early cinema two-strip color",
			SlopeR: 1.15, SlopeG: 0.90, SlopeB: 0.95,
			OffsetR: 0.02, OffsetG: 0.01, OffsetB: 0.01,
			PowerR: 0.88, PowerG: 1.05, PowerB: 1.0,
			Saturation: 1.20, Contrast: 0.15,
		},
		{
			Name: "daguerreotype", Group: "Vintage",
			Description: "Antique silver, almost monochrome",
			SlopeR: 1.02, SlopeG: 1.0, SlopeB: 0.98,
			OffsetR: 0.01, OffsetG: 0.01, OffsetB: 0.01,
			PowerR: 0.95, PowerG: 0.95, PowerB: 0.98,
			Saturation: 0.08, Contrast: 0.18,
		},

		// ── Clean (5) — professional, broadcast-ready ──────────────────
		{
			Name: "broadcast", Group: "Clean",
			Description: "Crisp, slightly cool, standard",
			SlopeR: 1.02, SlopeG: 1.02, SlopeB: 1.05,
			OffsetR: 0.0, OffsetG: 0.0, OffsetB: 0.01,
			PowerR: 0.98, PowerG: 0.98, PowerB: 0.96,
			Saturation: 0.95, Contrast: 0.12,
		},
		{
			Name: "natural", Group: "Clean",
			Description: "True-to-life, minimal intervention",
			SlopeR: 1.01, SlopeG: 1.01, SlopeB: 1.01,
			OffsetR: 0.0, OffsetG: 0.0, OffsetB: 0.0,
			PowerR: 0.99, PowerG: 0.99, PowerB: 0.99,
			Saturation: 1.02, Contrast: 0.05,
		},
		{
			Name: "clarity", Group: "Clean",
			Description: "Enhanced detail impression, clean",
			SlopeR: 1.05, SlopeG: 1.05, SlopeB: 1.05,
			OffsetR: 0.0, OffsetG: 0.0, OffsetB: 0.0,
			PowerR: 0.92, PowerG: 0.92, PowerB: 0.92,
			Saturation: 1.05, Contrast: 0.18,
		},
		{
			Name: "daylight", Group: "Clean",
			Description: "Balanced outdoor, slight warmth",
			SlopeR: 1.04, SlopeG: 1.02, SlopeB: 0.98,
			OffsetR: 0.01, OffsetG: 0.0, OffsetB: 0.0,
			PowerR: 0.96, PowerG: 0.98, PowerB: 1.01,
			Saturation: 1.03, Contrast: 0.08,
		},
		{
			Name: "studio", Group: "Clean",
			Description: "Controlled, even, neutral",
			SlopeR: 1.02, SlopeG: 1.02, SlopeB: 1.02,
			OffsetR: 0.0, OffsetG: 0.0, OffsetB: 0.0,
			PowerR: 0.96, PowerG: 0.96, PowerB: 0.96,
			Saturation: 0.98, Contrast: 0.10,
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
