// Package tracking provides camera tracking types and interfaces for AR
// graphics. It defines the CameraPose data structure that all tracking
// sources (Free-D hardware, AI visual, ArUco markers) produce, along
// with lens calibration intrinsics and the TrackingSource interface.
package tracking

import (
	"errors"
	"math"
)

// Sentinel errors for the tracking subsystem.
var (
	// ErrNoTracking indicates no tracking data is available for a source.
	ErrNoTracking = errors.New("tracking: no tracking data available")

	// ErrInvalidConfig indicates a tracking configuration is invalid.
	ErrInvalidConfig = errors.New("tracking: invalid configuration")

	// ErrMapNotReady indicates the studio map has not been built yet.
	ErrMapNotReady = errors.New("tracking: studio map not built")

	// ErrSourceExists indicates a tracking source with that name is already registered.
	ErrSourceExists = errors.New("tracking: source already registered")

	// ErrSourceNotFound indicates no tracking source with that name exists.
	ErrSourceNotFound = errors.New("tracking: source not found")
)

// CameraPose represents a camera's position and orientation in world space.
type CameraPose struct {
	// Position is the camera location as [X, Y, Z] in meters.
	Position [3]float64 `json:"position"`

	// Orientation is the camera rotation as [Pan, Tilt, Roll] in degrees.
	Orientation [3]float64 `json:"orientation"`

	// FocalLength is the current focal length in millimeters.
	FocalLength float64 `json:"focalLength"`

	// FocusDistance is the current focus distance in millimeters.
	// Zero means unknown.
	FocusDistance float64 `json:"focusDistance"`

	// Confidence is the tracking confidence from 0 (lost) to 1 (locked).
	Confidence float32 `json:"confidence"`

	// Timestamp is the presentation timestamp in 90 kHz clock units.
	Timestamp int64 `json:"timestamp"`

	// Source identifies the tracking system that produced this pose:
	// "freed", "visual", "aruco", or "hybrid".
	Source string `json:"source"`
}

// CameraIntrinsics holds per-camera lens calibration data used to
// convert tracking data into accurate projection matrices.
type CameraIntrinsics struct {
	// FocalLengthCurve maps zoom encoder values to focal lengths in mm.
	// Each entry is [zoomEncoderValue, focalLength_mm].
	// The curve is interpolated linearly at runtime.
	FocalLengthCurve [][2]float64 `json:"focalLengthCurve"`

	// PrincipalPoint is the optical center [cx, cy] normalized to 0-1.
	PrincipalPoint [2]float64 `json:"principalPoint"`

	// Distortion holds the Brown-Conrady distortion coefficients
	// [k1, k2, p1, p2, k3].
	Distortion [5]float64 `json:"distortion"`

	// SensorWidth is the physical sensor width in millimeters.
	// Zero means unknown.
	SensorWidth float64 `json:"sensorWidth"`
}

// FocalLengthAtZoom interpolates the focal length for a given zoom
// encoder value using the calibration curve. If the curve is empty
// it returns 0. If the curve has a single point, that focal length
// is returned regardless of zoom value. Values outside the curve
// range are clamped to the nearest endpoint.
func (c *CameraIntrinsics) FocalLengthAtZoom(zoom float64) float64 {
	n := len(c.FocalLengthCurve)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return c.FocalLengthCurve[0][1]
	}

	// Clamp to curve bounds.
	if zoom <= c.FocalLengthCurve[0][0] {
		return c.FocalLengthCurve[0][1]
	}
	if zoom >= c.FocalLengthCurve[n-1][0] {
		return c.FocalLengthCurve[n-1][1]
	}

	// Find the surrounding segment and interpolate.
	for i := 1; i < n; i++ {
		z0 := c.FocalLengthCurve[i-1][0]
		f0 := c.FocalLengthCurve[i-1][1]
		z1 := c.FocalLengthCurve[i][0]
		f1 := c.FocalLengthCurve[i][1]

		if zoom <= z1 {
			if math.Abs(z1-z0) < 1e-12 {
				return f0
			}
			t := (zoom - z0) / (z1 - z0)
			return f0 + t*(f1-f0)
		}
	}

	// Should not reach here, but return last value as safety.
	return c.FocalLengthCurve[n-1][1]
}

// TrackingSource provides camera pose data from a tracking system.
// Implementations include Free-D protocol receivers, AI visual
// trackers, and ArUco marker detectors.
type TrackingSource interface {
	// Name returns the unique name of this tracking source.
	Name() string

	// Type returns the tracking system type: "freed", "visual", or "aruco".
	Type() string

	// Start begins tracking. Returns an error if already running or
	// if initialization fails.
	Start() error

	// Stop halts tracking without releasing resources.
	Stop() error

	// Running reports whether the source is actively tracking.
	Running() bool

	// LatestPose returns the most recent camera pose, or nil if no
	// pose data is available yet.
	LatestPose() *CameraPose

	// Close releases all resources held by this source.
	Close() error
}

// TrackingSourceInfo is the JSON-serializable status of a registered
// tracking source, used in REST API responses and state broadcasts.
type TrackingSourceInfo struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Running    bool    `json:"running"`
	Confidence float32 `json:"confidence"`
}
