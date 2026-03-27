package tracking

import (
	"math"
	"math/rand"
	"testing"
)

func TestPoseFilter_ConstantInput(t *testing.T) {
	f := NewPoseFilter(0.01, 0.1)

	// Feed the same pose 50 times. The filter should converge to it.
	for i := 0; i < 50; i++ {
		pose := &CameraPose{
			Position:    [3]float64{1.0, 2.0, 3.0},
			Orientation: [3]float64{10.0, 20.0, 30.0},
			FocalLength: 50.0,
			Confidence:  1.0,
			Timestamp:   int64(i) * 3000, // 90kHz clock, ~33ms apart
			Source:      "test",
		}
		filtered := f.Update(pose)

		if filtered == nil {
			t.Fatalf("Update returned nil on iteration %d", i)
		}
	}

	// After convergence, output should be very close to input.
	final := f.Update(&CameraPose{
		Position:    [3]float64{1.0, 2.0, 3.0},
		Orientation: [3]float64{10.0, 20.0, 30.0},
		FocalLength: 50.0,
		Confidence:  1.0,
		Timestamp:   50 * 3000,
		Source:      "test",
	})

	const tol = 0.01
	for i, label := range []string{"X", "Y", "Z"} {
		if math.Abs(final.Position[i]-[3]float64{1.0, 2.0, 3.0}[i]) > tol {
			t.Errorf("Position %s: got %f, want %f (tol %f)", label, final.Position[i], [3]float64{1.0, 2.0, 3.0}[i], tol)
		}
	}
	for i, label := range []string{"Pan", "Tilt", "Roll"} {
		if math.Abs(final.Orientation[i]-[3]float64{10.0, 20.0, 30.0}[i]) > tol {
			t.Errorf("Orientation %s: got %f, want %f (tol %f)", label, final.Orientation[i], [3]float64{10.0, 20.0, 30.0}[i], tol)
		}
	}
}

func TestPoseFilter_SmoothsNoise(t *testing.T) {
	// Process noise must be high enough to track sinusoidal motion;
	// measurement noise set to match the actual noise variance.
	f := NewPoseFilter(1.0, 0.25)
	rng := rand.New(rand.NewSource(42))

	// Generate sinusoidal motion with noise.
	const (
		numSamples = 200
		amplitude  = 5.0
		noiseLevel = 0.5
		dt         = 3000 // ~33ms in 90kHz
	)

	rawErrors := make([]float64, 0, numSamples)
	filteredErrors := make([]float64, 0, numSamples)

	for i := 0; i < numSamples; i++ {
		// True position follows a sine wave.
		trueVal := amplitude * math.Sin(2*math.Pi*float64(i)/100.0)
		noise := noiseLevel * rng.NormFloat64()
		measured := trueVal + noise

		pose := &CameraPose{
			Position:    [3]float64{measured, 0, 0},
			Orientation: [3]float64{0, 0, 0},
			Timestamp:   int64(i) * dt,
			Confidence:  1.0,
			Source:      "test",
		}

		filtered := f.Update(pose)

		// Skip first few samples while filter warms up.
		if i > 20 {
			rawErrors = append(rawErrors, math.Abs(measured-trueVal))
			filteredErrors = append(filteredErrors, math.Abs(filtered.Position[0]-trueVal))
		}
	}

	// Compute mean absolute errors.
	rawMAE := mean(rawErrors)
	filteredMAE := mean(filteredErrors)

	t.Logf("Raw MAE: %f, Filtered MAE: %f", rawMAE, filteredMAE)

	if filteredMAE >= rawMAE {
		t.Errorf("Filter did not reduce noise: raw MAE=%f, filtered MAE=%f", rawMAE, filteredMAE)
	}
}

func TestPoseFilter_Reset(t *testing.T) {
	f := NewPoseFilter(0.01, 0.1)

	// Feed some data.
	for i := 0; i < 20; i++ {
		f.Update(&CameraPose{
			Position:  [3]float64{float64(i), 0, 0},
			Timestamp: int64(i) * 3000,
			Source:    "test",
		})
	}

	f.Reset()

	// After reset, the filter should behave as if freshly created.
	// First update after reset should return a value close to the measurement.
	first := f.Update(&CameraPose{
		Position:    [3]float64{100.0, 200.0, 300.0},
		Orientation: [3]float64{45.0, 0, 0},
		Timestamp:   0,
		Confidence:  1.0,
		Source:      "test",
	})

	if first == nil {
		t.Fatal("expected non-nil pose after reset")
	}

	// First measurement after reset should be passed through (or very close).
	const tol = 0.01
	if math.Abs(first.Position[0]-100.0) > tol {
		t.Errorf("after reset, Position.X = %f, want 100.0", first.Position[0])
	}
	if math.Abs(first.Orientation[0]-45.0) > tol {
		t.Errorf("after reset, Orientation.Pan = %f, want 45.0", first.Orientation[0])
	}
}

func TestPoseFilter_LargeTimeGap(t *testing.T) {
	f := NewPoseFilter(0.01, 0.1)

	// Feed some data at normal rate.
	for i := 0; i < 10; i++ {
		f.Update(&CameraPose{
			Position:  [3]float64{1.0, 0, 0},
			Timestamp: int64(i) * 3000,
			Source:    "test",
		})
	}

	// Now jump to a very different time and position.
	// The filter should handle this gracefully (not produce huge prediction errors).
	result := f.Update(&CameraPose{
		Position:  [3]float64{50.0, 0, 0},
		Timestamp: 90000 * 60, // 1 minute later
		Confidence: 1.0,
		Source:    "test",
	})

	if result == nil {
		t.Fatal("expected non-nil pose after time gap")
	}

	// After a large gap the filter should heavily weight the measurement.
	// Allow wider tolerance since the filter may still blend slightly.
	if math.Abs(result.Position[0]-50.0) > 5.0 {
		t.Errorf("after large time gap, Position.X = %f, want ~50.0", result.Position[0])
	}
}

func TestPoseFilter_PreservesMetadata(t *testing.T) {
	f := NewPoseFilter(0.01, 0.1)

	pose := &CameraPose{
		Position:     [3]float64{1.0, 2.0, 3.0},
		Orientation:  [3]float64{10.0, 20.0, 30.0},
		FocalLength:  85.0,
		FocusDistance: 2000.0,
		Confidence:   0.95,
		Timestamp:    12345,
		Source:       "visual",
	}

	result := f.Update(pose)

	// Metadata should be passed through unchanged.
	if result.FocalLength != 85.0 {
		t.Errorf("FocalLength = %f, want 85.0", result.FocalLength)
	}
	if result.FocusDistance != 2000.0 {
		t.Errorf("FocusDistance = %f, want 2000.0", result.FocusDistance)
	}
	if result.Confidence != 0.95 {
		t.Errorf("Confidence = %f, want 0.95", result.Confidence)
	}
	if result.Timestamp != 12345 {
		t.Errorf("Timestamp = %d, want 12345", result.Timestamp)
	}
	if result.Source != "visual" {
		t.Errorf("Source = %q, want %q", result.Source, "visual")
	}
}

func TestPoseFilter_FirstUpdateReturnsInput(t *testing.T) {
	f := NewPoseFilter(0.01, 0.1)

	pose := &CameraPose{
		Position:    [3]float64{42.0, 43.0, 44.0},
		Orientation: [3]float64{90.0, -45.0, 0.0},
		Timestamp:   1000,
		Confidence:  1.0,
		Source:      "test",
	}

	result := f.Update(pose)

	const tol = 0.001
	for i := range 3 {
		if math.Abs(result.Position[i]-pose.Position[i]) > tol {
			t.Errorf("Position[%d] = %f, want %f", i, result.Position[i], pose.Position[i])
		}
		if math.Abs(result.Orientation[i]-pose.Orientation[i]) > tol {
			t.Errorf("Orientation[%d] = %f, want %f", i, result.Orientation[i], pose.Orientation[i])
		}
	}
}

func TestAxisFilter_PredictCorrect(t *testing.T) {
	af := newAxisFilter(0.01, 0.1)

	// Initialize with position=0, velocity=0.
	af.init(0.0)

	// Feed measurements of linearly increasing position.
	// The filter should track velocity.
	for i := 1; i <= 50; i++ {
		dt := 1.0 / 30.0 // 30fps
		measurement := float64(i) * 2.0
		af.update(measurement, dt)
	}

	// After tracking linear motion, estimated velocity should be close to 2.0 * 30 = 60 per second.
	vel := af.x[1]
	if math.Abs(vel-60.0) > 5.0 {
		t.Errorf("velocity = %f, want ~60.0", vel)
	}
}

// mean computes the mean of a float64 slice.
func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}
