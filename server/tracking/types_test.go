package tracking

import (
	"encoding/json"
	"math"
	"testing"
)

func TestCameraPoseJSONRoundTrip(t *testing.T) {
	pose := CameraPose{
		Position:     [3]float64{1.5, 2.0, -3.25},
		Orientation:  [3]float64{45.0, -10.5, 0.0},
		FocalLength:  50.0,
		FocusDistance: 3000.0,
		Confidence:   0.95,
		Timestamp:    2700000,
		Source:       "freed",
	}

	data, err := json.Marshal(pose)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got CameraPose
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got != pose {
		t.Errorf("round-trip mismatch:\ngot  %+v\nwant %+v", got, pose)
	}

	// Verify JSON field names.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, key := range []string{"position", "orientation", "focalLength", "focusDistance", "confidence", "timestamp", "source"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON field %q", key)
		}
	}
}

func TestCameraIntrinsicsJSONRoundTrip(t *testing.T) {
	intrinsics := CameraIntrinsics{
		FocalLengthCurve: [][2]float64{
			{0.0, 18.0},
			{0.5, 50.0},
			{1.0, 135.0},
		},
		PrincipalPoint: [2]float64{0.5, 0.5},
		Distortion:     [5]float64{-0.1, 0.05, 0.001, -0.002, 0.0},
		SensorWidth:    36.0,
	}

	data, err := json.Marshal(intrinsics)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got CameraIntrinsics
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.FocalLengthCurve) != len(intrinsics.FocalLengthCurve) {
		t.Fatalf("curve length: got %d, want %d", len(got.FocalLengthCurve), len(intrinsics.FocalLengthCurve))
	}
	for i, pt := range got.FocalLengthCurve {
		if pt != intrinsics.FocalLengthCurve[i] {
			t.Errorf("curve[%d]: got %v, want %v", i, pt, intrinsics.FocalLengthCurve[i])
		}
	}
	if got.PrincipalPoint != intrinsics.PrincipalPoint {
		t.Errorf("principalPoint: got %v, want %v", got.PrincipalPoint, intrinsics.PrincipalPoint)
	}
	if got.Distortion != intrinsics.Distortion {
		t.Errorf("distortion: got %v, want %v", got.Distortion, intrinsics.Distortion)
	}
	if got.SensorWidth != intrinsics.SensorWidth {
		t.Errorf("sensorWidth: got %v, want %v", got.SensorWidth, intrinsics.SensorWidth)
	}

	// Verify JSON field names.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, key := range []string{"focalLengthCurve", "principalPoint", "distortion", "sensorWidth"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON field %q", key)
		}
	}
}

func TestFocalLengthAtZoom(t *testing.T) {
	tests := []struct {
		name  string
		curve [][2]float64
		zoom  float64
		want  float64
	}{
		{
			name:  "empty curve returns zero",
			curve: nil,
			zoom:  0.5,
			want:  0,
		},
		{
			name:  "single point returns that focal length",
			curve: [][2]float64{{0.5, 35.0}},
			zoom:  0.0,
			want:  35.0,
		},
		{
			name:  "single point any zoom value",
			curve: [][2]float64{{0.5, 35.0}},
			zoom:  1.0,
			want:  35.0,
		},
		{
			name:  "below range clamps to first",
			curve: [][2]float64{{0.0, 18.0}, {1.0, 135.0}},
			zoom:  -0.5,
			want:  18.0,
		},
		{
			name:  "above range clamps to last",
			curve: [][2]float64{{0.0, 18.0}, {1.0, 135.0}},
			zoom:  2.0,
			want:  135.0,
		},
		{
			name:  "exact first point",
			curve: [][2]float64{{0.0, 18.0}, {1.0, 135.0}},
			zoom:  0.0,
			want:  18.0,
		},
		{
			name:  "exact last point",
			curve: [][2]float64{{0.0, 18.0}, {1.0, 135.0}},
			zoom:  1.0,
			want:  135.0,
		},
		{
			name:  "midpoint two points",
			curve: [][2]float64{{0.0, 18.0}, {1.0, 135.0}},
			zoom:  0.5,
			want:  76.5, // 18 + 0.5*(135-18)
		},
		{
			name:  "quarter point",
			curve: [][2]float64{{0.0, 18.0}, {1.0, 135.0}},
			zoom:  0.25,
			want:  47.25, // 18 + 0.25*117
		},
		{
			name: "three-point curve first segment",
			curve: [][2]float64{
				{0.0, 18.0},
				{0.5, 50.0},
				{1.0, 135.0},
			},
			zoom: 0.25,
			want: 34.0, // 18 + 0.5*(50-18)
		},
		{
			name: "three-point curve second segment",
			curve: [][2]float64{
				{0.0, 18.0},
				{0.5, 50.0},
				{1.0, 135.0},
			},
			zoom: 0.75,
			want: 92.5, // 50 + 0.5*(135-50)
		},
		{
			name: "exact middle point of three",
			curve: [][2]float64{
				{0.0, 18.0},
				{0.5, 50.0},
				{1.0, 135.0},
			},
			zoom: 0.5,
			want: 50.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ci := &CameraIntrinsics{FocalLengthCurve: tt.curve}
			got := ci.FocalLengthAtZoom(tt.zoom)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("FocalLengthAtZoom(%v) = %v, want %v", tt.zoom, got, tt.want)
			}
		})
	}
}

func TestTrackingSourceInfoJSON(t *testing.T) {
	info := TrackingSourceInfo{
		Name:       "cam1-freed",
		Type:       "freed",
		Running:    true,
		Confidence: 0.98,
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got TrackingSourceInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got != info {
		t.Errorf("round-trip mismatch:\ngot  %+v\nwant %+v", got, info)
	}

	// Verify JSON field names.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, key := range []string{"name", "type", "running", "confidence"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON field %q", key)
		}
	}
}

func TestSentinelErrors(t *testing.T) {
	tests := []struct {
		err  error
		text string
	}{
		{ErrNoTracking, "tracking: no tracking data available"},
		{ErrInvalidConfig, "tracking: invalid configuration"},
		{ErrMapNotReady, "tracking: studio map not built"},
		{ErrSourceExists, "tracking: source already registered"},
		{ErrSourceNotFound, "tracking: source not found"},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.text {
				t.Errorf("error text = %q, want %q", got, tt.text)
			}
		})
	}
}
