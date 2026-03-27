package tracking

import (
	"math"
	"sync"
	"testing"
)

func TestArucoDetector_NewAndDefaults(t *testing.T) {
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		SensorWidth:      0,
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}

	d := NewArucoDetector(0.15, intrinsics)
	if d == nil {
		t.Fatal("NewArucoDetector returned nil")
	}
	if d.markerSize != 0.15 {
		t.Errorf("markerSize = %f, want 0.15", d.markerSize)
	}

	// No positions registered yet.
	pos := d.Positions()
	if len(pos) != 0 {
		t.Errorf("expected empty positions, got %d", len(pos))
	}
}

func TestArucoDetector_SetPositions(t *testing.T) {
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}
	d := NewArucoDetector(0.10, intrinsics)

	positions := map[int][3]float64{
		0: {1.0, 0.5, 0.0},
		5: {-2.0, 1.0, 3.0},
		9: {0.0, 0.0, 0.0},
	}
	d.SetPositions(positions)

	got := d.Positions()
	if len(got) != 3 {
		t.Fatalf("expected 3 positions, got %d", len(got))
	}
	for id, pos := range positions {
		g, ok := got[id]
		if !ok {
			t.Errorf("missing marker %d", id)
			continue
		}
		if g != pos {
			t.Errorf("marker %d: got %v, want %v", id, g, pos)
		}
	}

	// Verify returned map is a copy (mutations don't affect internal state).
	got[99] = [3]float64{99, 99, 99}
	if len(d.Positions()) != 3 {
		t.Error("Positions() returned a reference, not a copy")
	}
}

func TestArucoDetector_SetPositions_Overwrite(t *testing.T) {
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}
	d := NewArucoDetector(0.10, intrinsics)

	d.SetPositions(map[int][3]float64{0: {1, 2, 3}})
	d.SetPositions(map[int][3]float64{1: {4, 5, 6}})

	got := d.Positions()
	if len(got) != 1 {
		t.Fatalf("expected 1 position after overwrite, got %d", len(got))
	}
	if _, ok := got[1]; !ok {
		t.Error("expected marker 1 after overwrite")
	}
}

func TestArucoDetector_TrackingSourceInterface(t *testing.T) {
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}
	d := NewArucoDetector(0.10, intrinsics)

	// Compile-time interface check.
	var _ TrackingSource = d

	if d.Name() != "aruco" {
		t.Errorf("Name() = %q, want %q", d.Name(), "aruco")
	}
	if d.Type() != "aruco" {
		t.Errorf("Type() = %q, want %q", d.Type(), "aruco")
	}

	// Initially not running and no pose.
	if d.Running() {
		t.Error("Running() should be false before Start()")
	}
	if d.LatestPose() != nil {
		t.Error("LatestPose() should be nil before any processing")
	}

	// Start.
	if err := d.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	if !d.Running() {
		t.Error("Running() should be true after Start()")
	}

	// Double start returns error.
	if err := d.Start(); err == nil {
		t.Error("double Start() should return error")
	}

	// Stop.
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	if d.Running() {
		t.Error("Running() should be false after Stop()")
	}

	// Double stop is safe.
	if err := d.Stop(); err != nil {
		t.Fatalf("double Stop() = %v", err)
	}

	// Close.
	if err := d.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	// After close, Start fails.
	if err := d.Start(); err == nil {
		t.Error("Start() after Close() should return error")
	}

	// Close is idempotent.
	if err := d.Close(); err != nil {
		t.Fatalf("double Close() = %v", err)
	}
}

func TestArucoDetector_ProcessFrame_NoMarkers(t *testing.T) {
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}
	d := NewArucoDetector(0.10, intrinsics)
	d.SetPositions(map[int][3]float64{0: {1, 0, 0}})

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Process a uniform gray frame — no markers should be detected.
	w, h := 640, 480
	gray := make([]byte, w*h)
	for i := range gray {
		gray[i] = 128
	}

	d.ProcessFrame(gray, w, h, w)

	// No pose update expected since no markers detected.
	if d.LatestPose() != nil {
		t.Error("LatestPose() should be nil when no markers detected")
	}
}

func TestArucoDetector_ProcessFrame_NotRunning(t *testing.T) {
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}
	d := NewArucoDetector(0.10, intrinsics)

	// ProcessFrame while not running should be a no-op, not panic.
	gray := make([]byte, 640*480)
	d.ProcessFrame(gray, 640, 480, 640)

	if d.LatestPose() != nil {
		t.Error("LatestPose() should be nil when not running")
	}
}

func TestArucoDetector_ProcessDetections_PnPIntegration(t *testing.T) {
	// Test the ProcessDetections method that takes already-detected markers
	// and runs the PnP solve. This validates the full pipeline from marker
	// detections to camera pose without needing actual image processing.
	//
	// Markers must be placed on different planes (non-coplanar) for the
	// DLT-based PnP solver to work reliably.

	// Set up a known camera pose.
	truePan := 3.0
	trueTilt := 2.0
	trueRoll := 0.0
	truePos := [3]float64{0.0, 0.0, -5.0}

	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320.0, 240.0},
		SensorWidth:      0,
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}

	focalMM := 500.0
	fx := focalMM
	fy := fx
	cx := intrinsics.PrincipalPoint[0]
	cy := intrinsics.PrincipalPoint[1]

	markerSize := 0.15 // 15cm markers
	d := NewArucoDetector(markerSize, intrinsics)

	// Place markers on different surfaces (walls, ceiling) to break coplanarity.
	// DLT PnP needs non-coplanar point correspondences for accuracy.
	// 4 markers x 4 corners = 16 points, well above the 6-point minimum.
	markerPositions := map[int][3]float64{
		0: {-1.0, -1.0, 0.0},  // back wall, low-left
		1: {1.0, -1.0, 0.0},   // back wall, low-right
		2: {-1.0, 1.0, 0.0},   // back wall, high-left
		3: {0.0, 0.0, -1.0},   // side wall offset (different Z plane)
	}
	d.SetPositions(markerPositions)

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Generate synthetic 2D projections of marker corners.
	R := eulerToRotation(truePan, trueTilt, trueRoll)
	tVec := poseToTranslation([6]float64{truePos[0], truePos[1], truePos[2], truePan, trueTilt, trueRoll}, R)

	half := markerSize / 2.0
	markerIDs := []int{0, 1, 2, 3}
	detections := make([]MarkerDetection, len(markerIDs))
	for idx, id := range markerIDs {
		center := markerPositions[id]
		// Marker corners in 3D (flat planar marker):
		// top-left, top-right, bottom-right, bottom-left
		corners3D := [4][3]float64{
			{center[0] - half, center[1] + half, center[2]},
			{center[0] + half, center[1] + half, center[2]},
			{center[0] + half, center[1] - half, center[2]},
			{center[0] - half, center[1] - half, center[2]},
		}
		var corners2D [4][2]float64
		for i, c3d := range corners3D {
			corners2D[i] = projectPoint(c3d, R, tVec, fx, fy, cx, cy)
		}
		detections[idx] = MarkerDetection{
			ID:      id,
			Corners: corners2D,
		}
	}

	d.ProcessDetections(detections, 0)

	pose := d.LatestPose()
	if pose == nil {
		t.Fatal("LatestPose() should not be nil after processing valid detections")
	}

	// Verify source tag.
	if pose.Source != "aruco" {
		t.Errorf("Source = %q, want %q", pose.Source, "aruco")
	}

	// Verify position accuracy.
	posErr := math.Sqrt(
		sq(pose.Position[0]-truePos[0]) +
			sq(pose.Position[1]-truePos[1]) +
			sq(pose.Position[2]-truePos[2]),
	)

	panErr := math.Abs(angleDiff(pose.Orientation[0], truePan))
	tiltErr := math.Abs(angleDiff(pose.Orientation[1], trueTilt))
	rollErr := math.Abs(angleDiff(pose.Orientation[2], trueRoll))

	t.Logf("Position error: %.4f m (true: %v, solved: %v)", posErr, truePos, pose.Position)
	t.Logf("Pan error: %.4f deg", panErr)
	t.Logf("Tilt error: %.4f deg", tiltErr)
	t.Logf("Roll error: %.4f deg", rollErr)
	t.Logf("Confidence: %.4f", pose.Confidence)
	t.Logf("FocalLength: %.1f mm", pose.FocalLength)

	// Allow generous tolerance for DLT-based PnP with iterative refinement.
	if posErr > 0.50 {
		t.Errorf("position error %.4f m exceeds 50 cm tolerance", posErr)
	}
	if panErr > 10.0 {
		t.Errorf("pan error %.4f deg exceeds 10 deg tolerance", panErr)
	}
	if tiltErr > 10.0 {
		t.Errorf("tilt error %.4f deg exceeds 10 deg tolerance", tiltErr)
	}
	if rollErr > 10.0 {
		t.Errorf("roll error %.4f deg exceeds 10 deg tolerance", rollErr)
	}
	if pose.Confidence <= 0 {
		t.Error("confidence should be positive")
	}
	if pose.FocalLength != focalMM {
		t.Errorf("FocalLength = %f, want %f", pose.FocalLength, focalMM)
	}
}

func TestArucoDetector_ProcessDetections_TooFewPoints(t *testing.T) {
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}
	d := NewArucoDetector(0.10, intrinsics)
	d.SetPositions(map[int][3]float64{0: {1, 0, 0}})

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Only 1 marker = 4 points, which is below the 6-point PnP minimum.
	detections := []MarkerDetection{
		{ID: 0, Corners: [4][2]float64{{100, 100}, {200, 100}, {200, 200}, {100, 200}}},
	}
	d.ProcessDetections(detections, 0)

	// Should not crash, and should not update pose.
	if d.LatestPose() != nil {
		t.Error("LatestPose() should be nil with too few points for PnP")
	}
}

func TestArucoDetector_ProcessDetections_UnknownMarker(t *testing.T) {
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}
	d := NewArucoDetector(0.10, intrinsics)
	// Only marker 0 is registered.
	d.SetPositions(map[int][3]float64{0: {1, 0, 0}})

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Detect marker 99 which is not in positions.
	detections := []MarkerDetection{
		{ID: 99, Corners: [4][2]float64{{100, 100}, {200, 100}, {200, 200}, {100, 200}}},
	}
	d.ProcessDetections(detections, 0)

	if d.LatestPose() != nil {
		t.Error("LatestPose() should be nil when no known markers are detected")
	}
}

func TestArucoDetector_ProcessDetections_Empty(t *testing.T) {
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}
	d := NewArucoDetector(0.10, intrinsics)
	d.SetPositions(map[int][3]float64{0: {1, 0, 0}})

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Empty detection list.
	d.ProcessDetections(nil, 0)
	if d.LatestPose() != nil {
		t.Error("LatestPose() should be nil with empty detections")
	}

	d.ProcessDetections([]MarkerDetection{}, 0)
	if d.LatestPose() != nil {
		t.Error("LatestPose() should be nil with zero-length detections")
	}
}

func TestArucoDetector_ConcurrentAccess(t *testing.T) {
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}
	d := NewArucoDetector(0.10, intrinsics)

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	// Concurrent SetPositions.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			d.SetPositions(map[int][3]float64{id: {float64(id), 0, 0}})
		}(i)
	}

	// Concurrent Positions reads.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.Positions()
		}()
	}

	// Concurrent LatestPose reads.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.LatestPose()
		}()
	}

	// Concurrent ProcessFrame calls.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gray := make([]byte, 64*64)
			d.ProcessFrame(gray, 64, 64, 64)
		}()
	}

	wg.Wait()

	if err := d.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestArucoDetector_MarkerCorners3D(t *testing.T) {
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}
	d := NewArucoDetector(0.20, intrinsics)

	center := [3]float64{1.0, 2.0, 3.0}
	corners := d.markerCorners3D(center)

	half := 0.10
	expected := [4][3]float64{
		{1.0 - half, 2.0 + half, 3.0},
		{1.0 + half, 2.0 + half, 3.0},
		{1.0 + half, 2.0 - half, 3.0},
		{1.0 - half, 2.0 - half, 3.0},
	}

	for i, c := range corners {
		for j := 0; j < 3; j++ {
			if math.Abs(c[j]-expected[i][j]) > 1e-10 {
				t.Errorf("corner[%d][%d] = %f, want %f", i, j, c[j], expected[i][j])
			}
		}
	}
}

func TestArucoDetector_ManagerIntegration(t *testing.T) {
	// Verify ArucoDetector works with the tracking Manager.
	mgr := NewManager()
	defer mgr.Close()

	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}
	d := NewArucoDetector(0.15, intrinsics)

	err := mgr.AddSource("cam1", d)
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}

	// Duplicate add should fail.
	d2 := NewArucoDetector(0.15, intrinsics)
	err = mgr.AddSource("cam1", d2)
	if err != ErrSourceExists {
		t.Fatalf("expected ErrSourceExists, got: %v", err)
	}

	// Check Sources() includes the aruco tracker.
	sources := mgr.Sources()
	trackers, ok := sources["cam1"]
	if !ok {
		t.Fatal("cam1 not found in sources")
	}
	if len(trackers) != 1 {
		t.Fatalf("expected 1 tracker, got %d", len(trackers))
	}
	if trackers[0].Name != "aruco" || trackers[0].Type != "aruco" {
		t.Errorf("tracker info: %+v", trackers[0])
	}

	// Remove.
	err = mgr.RemoveSource("cam1", "aruco")
	if err != nil {
		t.Fatalf("RemoveSource: %v", err)
	}
}

func TestDetectMarkers_EmptyImage(t *testing.T) {
	// A blank image should return no detections.
	w, h := 320, 240
	gray := make([]byte, w*h)
	for i := range gray {
		gray[i] = 128 // uniform gray
	}

	detections := detectMarkers(gray, w, h, w)
	if len(detections) != 0 {
		t.Errorf("expected 0 detections on uniform image, got %d", len(detections))
	}
}

func TestDetectMarkers_TooSmallImage(t *testing.T) {
	// Images smaller than the block size should not panic.
	gray := make([]byte, 4*4)
	detections := detectMarkers(gray, 4, 4, 4)
	if len(detections) != 0 {
		t.Errorf("expected 0 detections on tiny image, got %d", len(detections))
	}
}

func TestAdaptiveThreshold(t *testing.T) {
	// Verify adaptive threshold doesn't panic and produces binary output.
	w, h := 64, 64
	gray := make([]byte, w*h)
	for i := range gray {
		if i%2 == 0 {
			gray[i] = 200
		} else {
			gray[i] = 50
		}
	}

	binary := adaptiveThreshold(gray, w, h, w, 7)
	if len(binary) != w*h {
		t.Fatalf("expected %d bytes, got %d", w*h, len(binary))
	}

	// All values should be 0 or 255.
	for i, v := range binary {
		if v != 0 && v != 255 {
			t.Errorf("binary[%d] = %d, expected 0 or 255", i, v)
			break
		}
	}
}

func TestAdaptiveThreshold_UniformImage(t *testing.T) {
	// A uniform image should produce all-zero output (no edges).
	w, h := 64, 64
	gray := make([]byte, w*h)
	for i := range gray {
		gray[i] = 128
	}

	binary := adaptiveThreshold(gray, w, h, w, 7)
	for i, v := range binary {
		if v != 0 {
			t.Errorf("binary[%d] = %d on uniform image, expected 0", i, v)
			break
		}
	}
}

func TestFindSquares_EmptyBinaryImage(t *testing.T) {
	w, h := 64, 64
	binary := make([]byte, w*h) // all black
	squares := findSquares(binary, w, h)
	if len(squares) != 0 {
		t.Errorf("expected 0 squares on empty image, got %d", len(squares))
	}
}

func TestDecodeMarkerBits(t *testing.T) {
	// Test the DICT_4X4_50 dictionary lookup.
	t.Run("known marker 0", func(t *testing.T) {
		bits := dict4x4_50[0]
		id, ok := lookupMarkerID(bits)
		if !ok {
			t.Fatal("marker 0 not found in dictionary")
		}
		if id != 0 {
			t.Errorf("expected ID 0, got %d", id)
		}
	})

	t.Run("unknown pattern", func(t *testing.T) {
		bits := [4][4]bool{
			{true, true, true, true},
			{true, true, true, true},
			{true, true, true, true},
			{true, true, true, true},
		}
		_, ok := lookupMarkerID(bits)
		if ok {
			t.Error("all-true pattern should not be in dictionary")
		}
	})

	t.Run("rotated marker", func(t *testing.T) {
		// Rotate marker 0 by 90 degrees and verify it's still detected.
		bits := dict4x4_50[0]
		rotated := rotateBits90(bits)
		id, ok := lookupMarkerID(rotated)
		if !ok {
			t.Fatal("rotated marker 0 not found")
		}
		if id != 0 {
			t.Errorf("expected ID 0, got %d", id)
		}
	})
}

// --- Tests for contour-based quad detection ---

func TestTraceContour_SimpleRectangle(t *testing.T) {
	// Create a 64x64 binary image with a white rectangle in the center.
	w, h := 64, 64
	binary := make([]byte, w*h)
	// Fill a 20x20 rectangle at (10,10)-(29,29).
	for y := 10; y < 30; y++ {
		for x := 10; x < 30; x++ {
			binary[y*w+x] = 255
		}
	}

	visited := make([]bool, w*h)
	contour := traceContour(binary, w, h, 10, 10, visited)

	// A 20x20 rectangle boundary should have roughly 4*(20-1) = 76 boundary points,
	// give or take for corners. Allow a range.
	if len(contour) < 40 {
		t.Errorf("contour too short: %d points (expected >= 40 for 20x20 rect)", len(contour))
	}
	if len(contour) > 200 {
		t.Errorf("contour too long: %d points (expected <= 200 for 20x20 rect)", len(contour))
	}

	// All contour points should be within the rectangle region.
	for _, p := range contour {
		if p[0] < 10 || p[0] >= 30 || p[1] < 10 || p[1] >= 30 {
			t.Errorf("contour point (%d, %d) outside rectangle bounds [10,30)", p[0], p[1])
			break
		}
	}
}

func TestDouglasPeucker_Rectangle(t *testing.T) {
	// Create a noisy rectangle contour and verify Douglas-Peucker simplifies to 4 points.
	var contour [][2]int

	// Top edge: (0,0) to (100,0)
	for x := 0; x <= 100; x++ {
		contour = append(contour, [2]int{x, 0})
	}
	// Right edge: (100,1) to (100,100)
	for y := 1; y <= 100; y++ {
		contour = append(contour, [2]int{100, y})
	}
	// Bottom edge: (99,100) to (0,100)
	for x := 99; x >= 0; x-- {
		contour = append(contour, [2]int{x, 100})
	}
	// Left edge: (0,99) to (0,1)
	for y := 99; y >= 1; y-- {
		contour = append(contour, [2]int{0, y})
	}

	result := douglasPeucker(contour, 5.0)

	if len(result) != 4 {
		t.Errorf("expected 4 points for rectangle, got %d: %v", len(result), result)
	}
}

func TestDouglasPeucker_NoisyRectangle(t *testing.T) {
	// Rectangle with slight wobble — should still simplify to 4.
	var contour [][2]int
	for x := 0; x <= 100; x++ {
		jitter := 0
		if x%7 == 0 {
			jitter = 1
		}
		contour = append(contour, [2]int{x, jitter})
	}
	for y := 1; y <= 100; y++ {
		contour = append(contour, [2]int{100, y})
	}
	for x := 99; x >= 0; x-- {
		contour = append(contour, [2]int{x, 100})
	}
	for y := 99; y >= 1; y-- {
		contour = append(contour, [2]int{0, y})
	}

	result := douglasPeucker(contour, 3.0)
	if len(result) != 4 {
		t.Errorf("expected 4 points for noisy rectangle, got %d", len(result))
	}
}

func TestFindQuads_AxisAlignedSquare(t *testing.T) {
	// Draw a white square on a black background and verify findQuads detects it.
	w, h := 100, 100
	binary := make([]byte, w*h)

	// Draw a 40x40 white square at (20,20)-(59,59).
	for y := 20; y < 60; y++ {
		for x := 20; x < 60; x++ {
			binary[y*w+x] = 255
		}
	}

	quads := findQuads(binary, w, h)
	if len(quads) == 0 {
		t.Fatal("expected at least 1 quad, got 0")
	}

	// The detected quad should roughly match our square.
	q := quads[0]
	for _, c := range q {
		if c[0] < 15 || c[0] > 65 || c[1] < 15 || c[1] > 65 {
			t.Errorf("corner (%f, %f) outside expected range", c[0], c[1])
		}
	}
}

func TestFindQuads_RotatedSquare(t *testing.T) {
	// Draw a rotated (45 degree) diamond shape and verify findQuads detects it.
	w, h := 100, 100
	binary := make([]byte, w*h)

	// Diamond centered at (50,50) with radius 20 pixels.
	cx, cy := 50.0, 50.0
	radius := 20.0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx := math.Abs(float64(x) - cx)
			dy := math.Abs(float64(y) - cy)
			if dx+dy <= radius {
				binary[y*w+x] = 255
			}
		}
	}

	quads := findQuads(binary, w, h)
	if len(quads) == 0 {
		t.Fatal("expected at least 1 quad for rotated square, got 0")
	}

	// Verify the detected quad is roughly a diamond.
	q := quads[0]
	area := quadArea(q)
	expectedArea := 2 * radius * radius // diamond area = 2*r^2
	if math.Abs(area-expectedArea)/expectedArea > 0.3 {
		t.Errorf("quad area %f differs too much from expected %f", area, expectedArea)
	}
}

func TestComputeHomography_Identity(t *testing.T) {
	// If corners map unit square to a specific rectangle, verify the transform.
	corners := [4][2]float64{
		{0, 0},     // TL
		{100, 0},   // TR
		{100, 100}, // BR
		{0, 100},   // BL
	}

	H := computeHomography(corners)

	// Map unit square corners and verify they match.
	testPts := [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {0.5, 0.5}}
	expected := [][2]float64{{0, 0}, {100, 0}, {100, 100}, {0, 100}, {50, 50}}

	for i, pt := range testPts {
		x, y, ok := applyHomography(H, pt[0], pt[1])
		if !ok {
			t.Errorf("applyHomography returned not ok for %v", pt)
			continue
		}
		dx := math.Abs(x - expected[i][0])
		dy := math.Abs(y - expected[i][1])
		if dx > 0.01 || dy > 0.01 {
			t.Errorf("point %v: got (%f, %f), want (%f, %f)", pt, x, y, expected[i][0], expected[i][1])
		}
	}
}

func TestComputeHomography_Perspective(t *testing.T) {
	// A trapezoid (perspective-distorted square) should still map correctly.
	corners := [4][2]float64{
		{20, 10},  // TL
		{80, 15},  // TR
		{90, 85},  // BR
		{10, 90},  // BL
	}

	H := computeHomography(corners)

	// Verify the 4 corners map correctly.
	src := [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	for i, s := range src {
		x, y, ok := applyHomography(H, s[0], s[1])
		if !ok {
			t.Errorf("applyHomography returned not ok for %v", s)
			continue
		}
		dx := math.Abs(x - corners[i][0])
		dy := math.Abs(y - corners[i][1])
		if dx > 0.01 || dy > 0.01 {
			t.Errorf("corner %d: got (%f, %f), want (%f, %f)", i, x, y, corners[i][0], corners[i][1])
		}
	}
}

// renderMarkerToImage renders a DICT_4X4_50 marker at the given 4 corner positions
// into a grayscale image. The marker is rendered with a 1-cell black border and
// 4x4 data cells, using the homography to perspective-project each pixel.
func renderMarkerToImage(gray []byte, w, h, pitch int, markerID int, corners [4][2]float64) {
	bits := dict4x4_50[markerID]

	// Compute the inverse homography: from image space to unit square.
	// We compute the forward homography (unit square -> image), then invert.
	Hfwd := computeHomography(corners)
	Hinv := invertHomography3x3(Hfwd)

	cellsTotal := 6.0

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Map this pixel to unit-square coordinates via inverse homography.
			u, v, ok := applyHomography(Hinv, float64(x), float64(y))
			if !ok {
				continue
			}

			// Check if inside the marker [0,1]x[0,1].
			if u < 0 || u > 1 || v < 0 || v > 1 {
				continue
			}

			// Determine which cell this maps to (6x6 grid).
			col := int(u * cellsTotal)
			row := int(v * cellsTotal)
			if col >= 6 {
				col = 5
			}
			if row >= 6 {
				row = 5
			}

			// Border cells are black.
			if col == 0 || col == 5 || row == 0 || row == 5 {
				gray[y*pitch+x] = 0
				continue
			}

			// Data cells.
			dataRow := row - 1
			dataCol := col - 1
			if bits[dataRow][dataCol] {
				gray[y*pitch+x] = 255 // white
			} else {
				gray[y*pitch+x] = 0 // black
			}
		}
	}
}

// invertHomography3x3 computes the inverse of a 3x3 matrix.
func invertHomography3x3(H [9]float64) [9]float64 {
	a, b, c := H[0], H[1], H[2]
	d, e, f := H[3], H[4], H[5]
	g, hh, i := H[6], H[7], H[8]

	det := a*(e*i-f*hh) - b*(d*i-f*g) + c*(d*hh-e*g)
	if math.Abs(det) < 1e-12 {
		return [9]float64{1, 0, 0, 0, 1, 0, 0, 0, 1} // fallback identity
	}

	invDet := 1.0 / det
	return [9]float64{
		(e*i - f*hh) * invDet, (c*hh - b*i) * invDet, (b*f - c*e) * invDet,
		(f*g - d*i) * invDet, (a*i - c*g) * invDet, (c*d - a*f) * invDet,
		(d*hh - e*g) * invDet, (b*g - a*hh) * invDet, (a*e - b*d) * invDet,
	}
}

func TestDetectMarkers_SyntheticAxisAligned(t *testing.T) {
	// Render marker 0 axis-aligned into a grayscale image and detect it.
	w, h := 200, 200
	pitch := w
	gray := make([]byte, w*h)
	// Fill with gray background.
	for i := range gray {
		gray[i] = 200
	}

	// Render marker 0 as a 60x60 square at center of image.
	corners := [4][2]float64{
		{70, 70},   // TL
		{130, 70},  // TR
		{130, 130}, // BR
		{70, 130},  // BL
	}
	renderMarkerToImage(gray, w, h, pitch, 0, corners)

	detections := detectMarkers(gray, w, h, pitch)
	if len(detections) == 0 {
		t.Fatal("expected at least 1 detection for axis-aligned marker 0")
	}

	found := false
	for _, d := range detections {
		if d.ID == 0 {
			found = true
			t.Logf("detected marker 0 at corners: %v", d.Corners)
			break
		}
	}
	if !found {
		t.Errorf("marker 0 not found in detections: %v", detections)
	}
}

func TestDetectMarkers_SyntheticRotated(t *testing.T) {
	// Render marker 1 rotated ~30 degrees and detect it.
	w, h := 200, 200
	pitch := w
	gray := make([]byte, w*h)
	for i := range gray {
		gray[i] = 200
	}

	// Rotate a 60x60 square centered at (100,100) by 30 degrees.
	cx, cy := 100.0, 100.0
	halfSize := 30.0
	angle := 30.0 * math.Pi / 180.0

	unitCorners := [4][2]float64{
		{-halfSize, -halfSize},
		{halfSize, -halfSize},
		{halfSize, halfSize},
		{-halfSize, halfSize},
	}
	var corners [4][2]float64
	cosA, sinA := math.Cos(angle), math.Sin(angle)
	for i, uc := range unitCorners {
		corners[i] = [2]float64{
			cx + uc[0]*cosA - uc[1]*sinA,
			cy + uc[0]*sinA + uc[1]*cosA,
		}
	}

	renderMarkerToImage(gray, w, h, pitch, 1, corners)

	detections := detectMarkers(gray, w, h, pitch)
	if len(detections) == 0 {
		t.Fatal("expected at least 1 detection for rotated marker 1")
	}

	found := false
	for _, d := range detections {
		if d.ID == 1 {
			found = true
			t.Logf("detected rotated marker 1 at corners: %v", d.Corners)
			break
		}
	}
	if !found {
		t.Errorf("marker 1 not found in detections (got %d detections): %v", len(detections), detections)
	}
}

func TestDetectMarkers_SyntheticPerspective(t *testing.T) {
	// Render marker 2 with perspective distortion (trapezoid) and detect it.
	w, h := 200, 200
	pitch := w
	gray := make([]byte, w*h)
	for i := range gray {
		gray[i] = 200
	}

	// Trapezoid: narrower at top, wider at bottom (simulates looking up at marker).
	corners := [4][2]float64{
		{80, 50},   // TL
		{120, 55},  // TR
		{140, 150}, // BR
		{60, 145},  // BL
	}
	renderMarkerToImage(gray, w, h, pitch, 2, corners)

	detections := detectMarkers(gray, w, h, pitch)
	if len(detections) == 0 {
		t.Fatal("expected at least 1 detection for perspective-distorted marker 2")
	}

	found := false
	for _, d := range detections {
		if d.ID == 2 {
			found = true
			t.Logf("detected perspective marker 2 at corners: %v", d.Corners)
			break
		}
	}
	if !found {
		t.Errorf("marker 2 not found in detections (got %d detections): %v", len(detections), detections)
	}
}

func TestDetectMarkers_MultipleMarkers(t *testing.T) {
	// Render two markers in the same image and verify both are detected.
	w, h := 400, 200
	pitch := w
	gray := make([]byte, w*h)
	for i := range gray {
		gray[i] = 200
	}

	// Marker 3 on the left.
	corners1 := [4][2]float64{
		{30, 50},  // TL
		{90, 50},  // TR
		{90, 110}, // BR
		{30, 110}, // BL
	}
	renderMarkerToImage(gray, w, h, pitch, 3, corners1)

	// Marker 5 on the right.
	corners2 := [4][2]float64{
		{250, 40},  // TL
		{340, 40},  // TR
		{340, 130}, // BR
		{250, 130}, // BL
	}
	renderMarkerToImage(gray, w, h, pitch, 5, corners2)

	detections := detectMarkers(gray, w, h, pitch)

	foundIDs := map[int]bool{}
	for _, d := range detections {
		foundIDs[d.ID] = true
		t.Logf("detected marker %d at corners: %v", d.ID, d.Corners)
	}

	if !foundIDs[3] {
		t.Error("marker 3 not detected")
	}
	if !foundIDs[5] {
		t.Error("marker 5 not detected")
	}
}

func TestIsConvexQuad(t *testing.T) {
	t.Run("convex square", func(t *testing.T) {
		q := [4][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
		if !isConvexQuad(q) {
			t.Error("expected convex")
		}
	})

	t.Run("non-convex (bowtie)", func(t *testing.T) {
		q := [4][2]float64{{0, 0}, {10, 10}, {10, 0}, {0, 10}}
		if isConvexQuad(q) {
			t.Error("expected non-convex for bowtie shape")
		}
	})
}

func TestQuadArea(t *testing.T) {
	q := [4][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	area := quadArea(q)
	if math.Abs(area-100.0) > 0.01 {
		t.Errorf("area = %f, want 100", area)
	}
}

func TestOrderCornersCW(t *testing.T) {
	// Scrambled corners of a square — should be reordered to start from TL, clockwise.
	corners := [4][2]float64{
		{10, 10}, // BR
		{0, 10},  // BL
		{0, 0},   // TL
		{10, 0},  // TR
	}
	ordered := orderCornersCW(corners)

	// First corner should be nearest top-left (0,0).
	if ordered[0][0] > 1 || ordered[0][1] > 1 {
		t.Errorf("first corner should be near (0,0), got (%f, %f)", ordered[0][0], ordered[0][1])
	}

	// Should be clockwise: TL, TR, BR, BL.
	// Verify by checking cross products are all same sign.
	if !isConvexQuad(ordered) {
		t.Error("ordered corners should form a convex quad")
	}
}
