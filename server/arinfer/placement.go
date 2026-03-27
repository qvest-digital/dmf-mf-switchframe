package arinfer

// PlacementResult holds the computed 3D position for placing an object.
type PlacementResult struct {
	Position [3]float64 `json:"position"` // X, Y, Z in meters
	Depth    float64    `json:"depth"`    // metric depth at the clicked pixel
}

// ComputePlacement converts a 2D pixel click + depth map value to a 3D position
// using a pinhole camera model.
//
// pixelX, pixelY: click coordinates in frame space (0-based)
// frameW, frameH: frame dimensions (e.g. 1920, 1080)
// depthValue: depth at that pixel (from Depth Anything v2, inverse relative units)
// focalMM: lens focal length in mm (default 50)
// sensorMM: sensor width in mm (default 36, full-frame equivalent)
func ComputePlacement(pixelX, pixelY, frameW, frameH int, depthValue float64, focalMM, sensorMM float64) PlacementResult {
	if focalMM <= 0 {
		focalMM = 50.0
	}
	if sensorMM <= 0 {
		sensorMM = 36.0
	}

	// Focal length in pixels: fx = f_mm * image_width / sensor_width_mm
	fx := focalMM * float64(frameW) / sensorMM
	fy := fx // square pixels

	// Principal point (frame center)
	cx := float64(frameW) / 2.0
	cy := float64(frameH) / 2.0

	// Depth Anything v2 outputs inverse depth (higher = closer).
	// Convert to metric depth estimate. Scale factor is approximate.
	// For a typical studio setup, depth range ~0.5m to ~10m.
	metricDepth := 3.0 / (depthValue + 0.01) // approximate inverse depth to meters

	// Project pixel to 3D using pinhole camera model (inverse projection)
	x := (float64(pixelX) - cx) * metricDepth / fx
	y := (float64(pixelY) - cy) * metricDepth / fy
	z := metricDepth

	return PlacementResult{
		Position: [3]float64{x, -y, z}, // flip Y (screen Y down, world Y up)
		Depth:    metricDepth,
	}
}
