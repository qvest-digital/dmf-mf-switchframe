package stmap

import (
	"math"
)

func init() {
	registerAnimatedGenerator(GeneratorInfo{
		Name:        "heat_shimmer",
		Description: "Per-row sinusoidal vertical displacement simulating heat haze",
		Type:        "animated",
		Params: map[string]ParamInfo{
			"intensity": {Description: "Shimmer intensity", Default: 0.3, Min: 0, Max: 1},
			"frequency": {Description: "Oscillation frequency (Hz)", Default: 2.0, Min: 0.5, Max: 10},
		},
	}, generateHeatShimmer)

	registerAnimatedGenerator(GeneratorInfo{
		Name:        "dream",
		Description: "Radial pull toward center with pulsing for a dreamy effect",
		Type:        "animated",
		Params: map[string]ParamInfo{
			"intensity": {Description: "Dream intensity", Default: 0.4, Min: 0, Max: 1},
		},
	}, generateDream)

	registerAnimatedGenerator(GeneratorInfo{
		Name:        "ripple",
		Description: "Concentric circular ripple displacement from a center point",
		Type:        "animated",
		Params: map[string]ParamInfo{
			"amplitude":  {Description: "Ripple amplitude in pixels", Default: 8, Min: 1, Max: 30},
			"wavelength": {Description: "Ripple wavelength in pixels", Default: 60, Min: 10, Max: 200},
			"cx":         {Description: "Center X (normalized 0-1)", Default: 0.5, Min: 0, Max: 1},
			"cy":         {Description: "Center Y (normalized 0-1)", Default: 0.5, Min: 0, Max: 1},
		},
	}, generateRipple)

	registerAnimatedGenerator(GeneratorInfo{
		Name:        "lens_breathe",
		Description: "Radial scale oscillation from center simulating breathing lens",
		Type:        "animated",
		Params: map[string]ParamInfo{
			"amplitude": {Description: "Scale amplitude", Default: 0.02, Min: 0, Max: 0.1},
			"frequency": {Description: "Breathing frequency (Hz)", Default: 0.5, Min: 0.1, Max: 5},
		},
	}, generateLensBreathe)

	registerAnimatedGenerator(GeneratorInfo{
		Name:        "vortex",
		Description: "Angular displacement increasing with distance from center",
		Type:        "animated",
		Params: map[string]ParamInfo{
			"intensity": {Description: "Vortex intensity", Default: 0.3, Min: 0, Max: 1},
		},
	}, generateVortex)
}

// generateHeatShimmer produces per-row sinusoidal vertical displacement.
func generateHeatShimmer(params map[string]float64, w, h, frameCount int) (*AnimatedSTMap, error) {
	intensity := params["intensity"]
	frequency := params["frequency"]

	identity := Identity(w, h)
	twoPi := 2.0 * math.Pi
	fh := float64(h)

	frames := make([]*STMap, frameCount)
	for f := 0; f < frameCount; f++ {
		phase := twoPi * float64(f) / float64(frameCount)

		s := make([]float32, w*h)
		t := make([]float32, w*h)

		// Copy identity S (no horizontal displacement).
		copy(s, identity.S)

		for y := 0; y < h; y++ {
			displacement := intensity * 0.01 * math.Sin(twoPi*frequency*float64(y)/fh+phase)
			dispF32 := float32(displacement)
			row := y * w
			for x := 0; x < w; x++ {
				t[row+x] = identity.T[row+x] + dispF32
			}
		}

		frames[f] = &STMap{
			Name:   "heat_shimmer",
			Width:  w,
			Height: h,
			S:      s,
			T:      t,
		}
	}

	return NewAnimatedSTMap("heat_shimmer", frames, 30), nil
}

// generateDream produces a radial pull toward center with pulsing.
func generateDream(params map[string]float64, w, h, frameCount int) (*AnimatedSTMap, error) {
	intensity := params["intensity"]

	identity := Identity(w, h)
	twoPi := 2.0 * math.Pi
	fw := float64(w)
	fh := float64(h)

	frames := make([]*STMap, frameCount)
	for f := 0; f < frameCount; f++ {
		pulse := 0.5 + 0.5*math.Sin(twoPi*float64(f)/float64(frameCount))

		s := make([]float32, w*h)
		t := make([]float32, w*h)

		for y := 0; y < h; y++ {
			row := y * w
			dy := (float64(y)+0.5)/fh - 0.5
			for x := 0; x < w; x++ {
				idx := row + x
				dx := (float64(x)+0.5)/fw - 0.5
				r := math.Sqrt(dx*dx + dy*dy)
				pull := intensity * 0.1 * pulse * r
				rSafe := math.Max(r, 0.001)
				s[idx] = identity.S[idx] - float32(pull*dx/rSafe)
				t[idx] = identity.T[idx] - float32(pull*dy/rSafe)
			}
		}

		frames[f] = &STMap{
			Name:   "dream",
			Width:  w,
			Height: h,
			S:      s,
			T:      t,
		}
	}

	return NewAnimatedSTMap("dream", frames, 30), nil
}

// generateRipple produces concentric circular displacement from a center point.
func generateRipple(params map[string]float64, w, h, frameCount int) (*AnimatedSTMap, error) {
	amplitude := params["amplitude"]
	wavelength := params["wavelength"]
	cx := params["cx"]
	cy := params["cy"]

	identity := Identity(w, h)
	twoPi := 2.0 * math.Pi
	fw := float64(w)
	fh := float64(h)

	frames := make([]*STMap, frameCount)
	for f := 0; f < frameCount; f++ {
		phase := twoPi * float64(f) / float64(frameCount)

		s := make([]float32, w*h)
		t := make([]float32, w*h)

		for y := 0; y < h; y++ {
			row := y * w
			dy := (float64(y) + 0.5) - cy*fh
			for x := 0; x < w; x++ {
				idx := row + x
				dx := (float64(x) + 0.5) - cx*fw
				r := math.Sqrt(dx*dx + dy*dy)

				if r > 0 {
					disp := amplitude * math.Sin(twoPi*r/wavelength+phase) / r
					s[idx] = identity.S[idx] + float32(disp*dx/fw)
					t[idx] = identity.T[idx] + float32(disp*dy/fh)
				} else {
					s[idx] = identity.S[idx]
					t[idx] = identity.T[idx]
				}
			}
		}

		frames[f] = &STMap{
			Name:   "ripple",
			Width:  w,
			Height: h,
			S:      s,
			T:      t,
		}
	}

	return NewAnimatedSTMap("ripple", frames, 30), nil
}

// generateLensBreathe produces radial scale oscillation from center.
func generateLensBreathe(params map[string]float64, w, h, frameCount int) (*AnimatedSTMap, error) {
	amplitude := params["amplitude"]
	frequency := params["frequency"]

	identity := Identity(w, h)
	twoPi := 2.0 * math.Pi

	frames := make([]*STMap, frameCount)
	for f := 0; f < frameCount; f++ {
		scale := 1.0 + amplitude*math.Sin(twoPi*frequency*float64(f)/float64(frameCount))

		s := make([]float32, w*h)
		t := make([]float32, w*h)

		for i := 0; i < w*h; i++ {
			s[i] = float32(0.5 + (float64(identity.S[i])-0.5)*scale)
			t[i] = float32(0.5 + (float64(identity.T[i])-0.5)*scale)
		}

		frames[f] = &STMap{
			Name:   "lens_breathe",
			Width:  w,
			Height: h,
			S:      s,
			T:      t,
		}
	}

	return NewAnimatedSTMap("lens_breathe", frames, 30), nil
}

// generateVortex produces angular displacement increasing with distance.
func generateVortex(params map[string]float64, w, h, frameCount int) (*AnimatedSTMap, error) {
	intensity := params["intensity"]

	twoPi := 2.0 * math.Pi
	fw := float64(w)
	fh := float64(h)

	frames := make([]*STMap, frameCount)
	for f := 0; f < frameCount; f++ {
		phase := twoPi * float64(f) / float64(frameCount)

		s := make([]float32, w*h)
		t := make([]float32, w*h)

		for y := 0; y < h; y++ {
			row := y * w
			dy := (float64(y)+0.5)/fh - 0.5
			for x := 0; x < w; x++ {
				idx := row + x
				dx := (float64(x)+0.5)/fw - 0.5
				r := math.Sqrt(dx*dx + dy*dy)
				angle := intensity * r * math.Sin(phase)
				cosA := math.Cos(angle)
				sinA := math.Sin(angle)
				newDx := dx*cosA - dy*sinA
				newDy := dx*sinA + dy*cosA
				s[idx] = float32(newDx + 0.5)
				t[idx] = float32(newDy + 0.5)
			}
		}

		frames[f] = &STMap{
			Name:   "vortex",
			Width:  w,
			Height: h,
			S:      s,
			T:      t,
		}
	}

	return NewAnimatedSTMap("vortex", frames, 30), nil
}
