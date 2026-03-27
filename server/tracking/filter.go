package tracking

// PoseFilter smooths camera pose estimates using a Kalman filter.
// It operates on each of the 6 axes (pan, tilt, roll, X, Y, Z)
// independently with a constant-velocity motion model.
type PoseFilter struct {
	filters [6]*axisFilter // pan, tilt, roll, x, y, z
	lastTS  int64
	inited  bool
}

// axisFilter is a 2-state (position, velocity) linear Kalman filter
// for a single axis.
type axisFilter struct {
	x [2]float64    // state: [position, velocity]
	P [2][2]float64 // error covariance
	Q [2][2]float64 // process noise covariance
	R float64       // measurement noise variance

	inited bool
}

// maxDT caps the time delta used in prediction to avoid instability
// after long gaps. If dt exceeds this, the filter resets to the
// new measurement instead of predicting forward.
const maxDT = 2.0 // seconds

// NewPoseFilter creates a Kalman filter for 6DoF pose smoothing.
// processNoise controls how much the filter trusts the motion model
// (lower = smoother). measurementNoise controls how much the filter
// trusts new measurements (higher = smoother).
func NewPoseFilter(processNoise, measurementNoise float64) *PoseFilter {
	f := &PoseFilter{}
	for i := range f.filters {
		f.filters[i] = newAxisFilter(processNoise, measurementNoise)
	}
	return f
}

// Update filters a new pose measurement and returns the smoothed result.
// The original pose is not modified; a new CameraPose is returned.
// Metadata fields (FocalLength, FocusDistance, Confidence, Source)
// are passed through unchanged.
func (f *PoseFilter) Update(pose *CameraPose) *CameraPose {
	// Compute dt in seconds from 90kHz timestamps.
	var dt float64
	if f.inited {
		dt = float64(pose.Timestamp-f.lastTS) / 90000.0
	}
	f.lastTS = pose.Timestamp

	// Extract the 6 axes: orientation (pan, tilt, roll), position (x, y, z).
	measurements := [6]float64{
		pose.Orientation[0], pose.Orientation[1], pose.Orientation[2],
		pose.Position[0], pose.Position[1], pose.Position[2],
	}

	var filtered [6]float64
	for i, af := range f.filters {
		if !f.inited {
			af.init(measurements[i])
			filtered[i] = measurements[i]
		} else {
			filtered[i] = af.update(measurements[i], dt)
		}
	}

	f.inited = true

	return &CameraPose{
		Position:     [3]float64{filtered[3], filtered[4], filtered[5]},
		Orientation:  [3]float64{filtered[0], filtered[1], filtered[2]},
		FocalLength:  pose.FocalLength,
		FocusDistance: pose.FocusDistance,
		Confidence:   pose.Confidence,
		Timestamp:    pose.Timestamp,
		Source:       pose.Source,
	}
}

// Reset clears the filter state so the next Update acts as the
// first measurement.
func (f *PoseFilter) Reset() {
	f.inited = false
	f.lastTS = 0
	for _, af := range f.filters {
		af.inited = false
		af.x = [2]float64{}
		af.P = [2][2]float64{}
	}
}

// newAxisFilter creates a single-axis Kalman filter.
func newAxisFilter(processNoise, measurementNoise float64) *axisFilter {
	return &axisFilter{
		Q: [2][2]float64{
			{processNoise, 0},
			{0, processNoise},
		},
		R: measurementNoise,
	}
}

// init sets the filter to an initial measurement with high covariance
// (maximum uncertainty on velocity).
func (af *axisFilter) init(measurement float64) {
	af.x = [2]float64{measurement, 0}
	af.P = [2][2]float64{
		{af.R, 0},
		{0, 100.0}, // high initial uncertainty on velocity
	}
	af.inited = true
}

// update performs one Kalman predict+correct cycle and returns the
// filtered position estimate.
func (af *axisFilter) update(measurement, dt float64) float64 {
	if !af.inited {
		af.init(measurement)
		return measurement
	}

	// If dt is too large or non-positive, reinitialize from measurement.
	if dt <= 0 || dt > maxDT {
		af.init(measurement)
		return measurement
	}

	// --- Predict ---
	// State transition: x' = F*x where F = [[1, dt], [0, 1]]
	xPred := [2]float64{
		af.x[0] + af.x[1]*dt,
		af.x[1],
	}

	// Covariance prediction: P' = F*P*F' + Q
	// F*P:
	fp00 := af.P[0][0] + dt*af.P[1][0]
	fp01 := af.P[0][1] + dt*af.P[1][1]
	fp10 := af.P[1][0]
	fp11 := af.P[1][1]

	// F*P*F':
	pPred := [2][2]float64{
		{fp00 + fp01*dt + af.Q[0][0], fp01 + af.Q[0][1]},
		{fp10 + fp11*dt + af.Q[1][0], fp11 + af.Q[1][1]},
	}

	// --- Correct ---
	// Measurement model: z = H*x where H = [1, 0]
	// Innovation: y = z - H*xPred
	y := measurement - xPred[0]

	// Innovation covariance: S = H*P'*H' + R = P'[0][0] + R
	S := pPred[0][0] + af.R

	// Kalman gain: K = P'*H'/S = [P'[0][0]/S, P'[1][0]/S]
	K := [2]float64{
		pPred[0][0] / S,
		pPred[1][0] / S,
	}

	// Updated state: x = xPred + K*y
	af.x[0] = xPred[0] + K[0]*y
	af.x[1] = xPred[1] + K[1]*y

	// Updated covariance: P = (I - K*H) * P'
	af.P[0][0] = (1 - K[0]) * pPred[0][0]
	af.P[0][1] = (1 - K[0]) * pPred[0][1]
	af.P[1][0] = pPred[1][0] - K[1]*pPred[0][0]
	af.P[1][1] = pPred[1][1] - K[1]*pPred[0][1]

	return af.x[0]
}
