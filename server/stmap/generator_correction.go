package stmap

import "math"

func init() {
	// 1. identity — no-op passthrough.
	registerGenerator(GeneratorInfo{
		Name:        "identity",
		Description: "Identity map (no distortion)",
		Type:        "static",
		Params:      map[string]ParamInfo{},
	}, generateIdentity)

	// 2. barrel — Brown-Conrady barrel distortion.
	registerGenerator(GeneratorInfo{
		Name:        "barrel",
		Description: "Barrel distortion (Brown-Conrady radial model)",
		Type:        "static",
		Params: map[string]ParamInfo{
			"k1": {Description: "Primary radial coefficient", Default: -0.3, Min: -1, Max: 0},
			"k2": {Description: "Secondary radial coefficient", Default: 0.0, Min: -1, Max: 1},
		},
	}, generateBarrel)

	// 3. pincushion — positive radial distortion.
	registerGenerator(GeneratorInfo{
		Name:        "pincushion",
		Description: "Pincushion distortion (positive radial model)",
		Type:        "static",
		Params: map[string]ParamInfo{
			"k1": {Description: "Primary radial coefficient", Default: 0.3, Min: 0, Max: 1},
			"k2": {Description: "Secondary radial coefficient", Default: 0.0, Min: -1, Max: 1},
		},
	}, generatePincushion)

	// 4. fisheye_to_rectilinear — equidistant fisheye inversion.
	registerGenerator(GeneratorInfo{
		Name:        "fisheye_to_rectilinear",
		Description: "Convert equidistant fisheye to rectilinear projection",
		Type:        "static",
		Params: map[string]ParamInfo{
			"fov": {Description: "Field of view in degrees", Default: 180, Min: 60, Max: 220},
		},
	}, generateFisheyeToRectilinear)

	// 5. corner_pin — bilinear perspective interpolation.
	registerGenerator(GeneratorInfo{
		Name:        "corner_pin",
		Description: "Four-corner pin perspective warp",
		Type:        "static",
		Params: map[string]ParamInfo{
			"tl_x": {Description: "Top-left X (0-1)", Default: 0, Min: 0, Max: 1},
			"tl_y": {Description: "Top-left Y (0-1)", Default: 0, Min: 0, Max: 1},
			"tr_x": {Description: "Top-right X (0-1)", Default: 1, Min: 0, Max: 1},
			"tr_y": {Description: "Top-right Y (0-1)", Default: 0, Min: 0, Max: 1},
			"bl_x": {Description: "Bottom-left X (0-1)", Default: 0, Min: 0, Max: 1},
			"bl_y": {Description: "Bottom-left Y (0-1)", Default: 1, Min: 0, Max: 1},
			"br_x": {Description: "Bottom-right X (0-1)", Default: 1, Min: 0, Max: 1},
			"br_y": {Description: "Bottom-right Y (0-1)", Default: 1, Min: 0, Max: 1},
		},
	}, generateCornerPin)
}

func generateIdentity(_ map[string]float64, w, h int) (*STMap, error) {
	return Identity(w, h), nil
}

// generateBarrel applies Brown-Conrady barrel distortion with negative k1.
func generateBarrel(params map[string]float64, w, h int) (*STMap, error) {
	return radialDistortion(params, w, h)
}

// generatePincushion applies Brown-Conrady radial distortion with positive k1.
func generatePincushion(params map[string]float64, w, h int) (*STMap, error) {
	return radialDistortion(params, w, h)
}

// radialDistortion implements the Brown-Conrady model shared by barrel and pincushion.
func radialDistortion(params map[string]float64, w, h int) (*STMap, error) {
	k1 := paramOr(params, "k1", 0)
	k2 := paramOr(params, "k2", 0)

	fw := float64(w)
	fh := float64(h)
	cx := fw / 2.0
	cy := fh / 2.0
	maxR := math.Max(cx, cy)

	n := w * h
	s := make([]float32, n)
	t := make([]float32, n)

	for y := 0; y < h; y++ {
		row := y * w
		for x := 0; x < w; x++ {
			dx := (float64(x) - cx) / maxR
			dy := (float64(y) - cy) / maxR
			r2 := dx*dx + dy*dy
			factor := 1.0 + k1*r2 + k2*r2*r2
			srcX := cx + dx*factor*maxR
			srcY := cy + dy*factor*maxR

			idx := row + x
			s[idx] = float32((srcX + 0.5) / fw)
			t[idx] = float32((srcY + 0.5) / fh)
		}
	}

	return &STMap{
		Width:  w,
		Height: h,
		S:      s,
		T:      t,
	}, nil
}

// generateFisheyeToRectilinear inverts equidistant fisheye to rectilinear.
func generateFisheyeToRectilinear(params map[string]float64, w, h int) (*STMap, error) {
	fovDeg := paramOr(params, "fov", 180)
	fovRad := fovDeg * math.Pi / 180.0

	fw := float64(w)
	fh := float64(h)
	cx := fw / 2.0
	cy := fh / 2.0
	maxR := math.Max(cx, cy)

	n := w * h
	s := make([]float32, n)
	t := make([]float32, n)

	for y := 0; y < h; y++ {
		row := y * w
		for x := 0; x < w; x++ {
			dx := (float64(x) - cx) / maxR
			dy := (float64(y) - cy) / maxR
			r := math.Sqrt(dx*dx + dy*dy)

			var scale float64
			if r > 0 {
				theta := math.Atan(r) * 2.0 / fovRad
				scale = theta / r
			} else {
				scale = 1.0
			}

			srcX := cx + dx*scale*maxR
			srcY := cy + dy*scale*maxR

			idx := row + x
			s[idx] = float32((srcX + 0.5) / fw)
			t[idx] = float32((srcY + 0.5) / fh)
		}
	}

	return &STMap{
		Width:  w,
		Height: h,
		S:      s,
		T:      t,
	}, nil
}

// generateCornerPin implements bilinear perspective interpolation from 4 corners.
func generateCornerPin(params map[string]float64, w, h int) (*STMap, error) {
	tlX := paramOr(params, "tl_x", 0)
	tlY := paramOr(params, "tl_y", 0)
	trX := paramOr(params, "tr_x", 1)
	trY := paramOr(params, "tr_y", 0)
	blX := paramOr(params, "bl_x", 0)
	blY := paramOr(params, "bl_y", 1)
	brX := paramOr(params, "br_x", 1)
	brY := paramOr(params, "br_y", 1)

	n := w * h
	s := make([]float32, n)
	t := make([]float32, n)

	wm1 := float64(w - 1)
	hm1 := float64(h - 1)

	for y := 0; y < h; y++ {
		row := y * w
		v := float64(y) / hm1
		for x := 0; x < w; x++ {
			u := float64(x) / wm1

			srcU := (1-v)*((1-u)*tlX+u*trX) + v*((1-u)*blX+u*brX)
			srcV := (1-v)*((1-u)*tlY+u*trY) + v*((1-u)*blY+u*brY)

			idx := row + x
			s[idx] = float32(srcU)
			t[idx] = float32(srcV)
		}
	}

	return &STMap{
		Width:  w,
		Height: h,
		S:      s,
		T:      t,
	}, nil
}
