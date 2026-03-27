package tracking

import (
	"errors"
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

// Sentinel errors for the ArUco detector.
var (
	errArucoAlreadyRunning = errors.New("aruco: detector already running")
	errArucoClosed         = errors.New("aruco: detector closed")
)

// MarkerDetection represents a single detected ArUco marker in an image.
type MarkerDetection struct {
	// ID is the marker identifier from the ArUco dictionary.
	ID int

	// Corners are the four detected 2D corner positions in pixel coordinates,
	// ordered: top-left, top-right, bottom-right, bottom-left.
	Corners [4][2]float64
}

// ArucoDetector detects ArUco markers and provides drift-correction poses
// via PnP solving against known 3D marker positions. It implements the
// TrackingSource interface.
type ArucoDetector struct {
	markerSize float64          // physical marker size in meters
	intrinsics CameraIntrinsics // camera calibration

	mu        sync.RWMutex
	positions map[int][3]float64 // markerID → 3D center position in studio coords

	latest  atomic.Pointer[CameraPose]
	running atomic.Bool
	closed  atomic.Bool
}

// NewArucoDetector creates a new ArUco marker detector.
// markerSize is the physical side length of each marker in meters.
// intrinsics provides the camera lens calibration data.
func NewArucoDetector(markerSize float64, intrinsics CameraIntrinsics) *ArucoDetector {
	return &ArucoDetector{
		markerSize: markerSize,
		intrinsics: intrinsics,
		positions:  make(map[int][3]float64),
	}
}

// SetPositions sets the known 3D positions of ArUco markers in the studio.
// Each entry maps a marker ID to its center position [X, Y, Z] in meters.
// Calling this replaces all previously registered positions.
func (a *ArucoDetector) SetPositions(positions map[int][3]float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.positions = make(map[int][3]float64, len(positions))
	for id, pos := range positions {
		a.positions[id] = pos
	}
}

// Positions returns a copy of the currently registered marker positions.
func (a *ArucoDetector) Positions() map[int][3]float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := make(map[int][3]float64, len(a.positions))
	for id, pos := range a.positions {
		result[id] = pos
	}
	return result
}

// Name returns the tracking source name.
func (a *ArucoDetector) Name() string { return "aruco" }

// Type returns the tracking source type.
func (a *ArucoDetector) Type() string { return "aruco" }

// Start begins the ArUco detection pipeline.
func (a *ArucoDetector) Start() error {
	if a.closed.Load() {
		return errArucoClosed
	}
	if a.running.Load() {
		return errArucoAlreadyRunning
	}
	a.running.Store(true)
	return nil
}

// Stop halts the detection pipeline without releasing resources.
func (a *ArucoDetector) Stop() error {
	a.running.Store(false)
	return nil
}

// Running reports whether the detector is active.
func (a *ArucoDetector) Running() bool {
	return a.running.Load()
}

// LatestPose returns the most recent camera pose derived from marker detections,
// or nil if no valid pose has been computed yet.
func (a *ArucoDetector) LatestPose() *CameraPose {
	return a.latest.Load()
}

// Close stops the detector and releases all resources.
func (a *ArucoDetector) Close() error {
	if a.closed.Swap(true) {
		return nil
	}
	a.running.Store(false)
	return nil
}

// ProcessFrame analyzes a grayscale image for ArUco markers and updates
// the camera pose if enough markers are detected for PnP solving.
// y is the Y plane data, w x h pixels with the given row pitch.
func (a *ArucoDetector) ProcessFrame(y []byte, w, h, pitch int) {
	if !a.running.Load() {
		return
	}

	detections := detectMarkers(y, w, h, pitch)
	if len(detections) == 0 {
		return
	}

	a.ProcessDetections(detections, 0)
}

// ProcessDetections takes pre-detected markers and runs PnP to compute a
// camera pose. This allows external callers to feed marker detections
// directly (e.g., from GPU-accelerated detection or test fixtures).
// timestamp is the frame presentation timestamp in 90 kHz clock units.
func (a *ArucoDetector) ProcessDetections(detections []MarkerDetection, timestamp int64) {
	if !a.running.Load() {
		return
	}
	if len(detections) == 0 {
		return
	}

	// Snapshot current marker positions.
	a.mu.RLock()
	positions := make(map[int][3]float64, len(a.positions))
	for id, pos := range a.positions {
		positions[id] = pos
	}
	a.mu.RUnlock()

	// Build 2D-3D correspondences from detections and known positions.
	var points2D [][2]float64
	var points3D [][3]float64

	for _, det := range detections {
		center, ok := positions[det.ID]
		if !ok {
			continue // unknown marker, skip
		}

		// Generate 3D corners from center + marker size.
		corners3D := a.markerCorners3D(center)

		for i := 0; i < 4; i++ {
			points2D = append(points2D, det.Corners[i])
			points3D = append(points3D, corners3D[i])
		}
	}

	// PnP requires at least 6 correspondences.
	if len(points2D) < 6 {
		return
	}

	// Determine focal length from intrinsics.
	focalMM := 0.0
	if len(a.intrinsics.FocalLengthCurve) > 0 {
		focalMM = a.intrinsics.FocalLengthCurve[0][1]
	}
	if focalMM <= 0 {
		return
	}

	pose, err := SolvePnP(points2D, points3D, a.intrinsics, focalMM)
	if err != nil {
		return
	}

	// Override source tag and timestamp.
	pose.Source = "aruco"
	pose.Timestamp = timestamp
	pose.FocalLength = focalMM

	a.latest.Store(pose)
}

// markerCorners3D computes the 4 corner positions of a marker in 3D space
// given its center position. The marker is assumed to be planar (facing the
// camera, lying in the XY plane relative to its center).
// Returns corners in order: top-left, top-right, bottom-right, bottom-left.
func (a *ArucoDetector) markerCorners3D(center [3]float64) [4][3]float64 {
	half := a.markerSize / 2.0
	return [4][3]float64{
		{center[0] - half, center[1] + half, center[2]}, // top-left
		{center[0] + half, center[1] + half, center[2]}, // top-right
		{center[0] + half, center[1] - half, center[2]}, // bottom-right
		{center[0] - half, center[1] - half, center[2]}, // bottom-left
	}
}

// --- ArUco detection pipeline (pure Go) ---

// detectMarkers scans a grayscale image for ArUco markers from DICT_4X4_50.
// gray is the image data, w x h pixels with the given row pitch (stride).
func detectMarkers(gray []byte, w, h, pitch int) []MarkerDetection {
	if w < 16 || h < 16 {
		return nil
	}

	// Step 1: Adaptive threshold to binary.
	binary := adaptiveThreshold(gray, w, h, pitch, 7)

	// Step 2: Find quadrilateral contours.
	quads := findSquares(binary, w, h)

	if len(quads) == 0 {
		return nil
	}

	// Step 3: For each quad, extract the inner bit pattern and identify the marker.
	var detections []MarkerDetection
	for _, quad := range quads {
		id, ok := decodeMarker(gray, w, h, pitch, quad)
		if !ok {
			continue
		}
		detections = append(detections, MarkerDetection{
			ID:      id,
			Corners: quad,
		})
	}

	return detections
}

// adaptiveThreshold applies a mean-based adaptive threshold to a grayscale image.
// Pixels darker than the local mean minus a constant become 255 (foreground),
// others become 0 (background). blockSize must be odd.
func adaptiveThreshold(gray []byte, w, h, pitch int, blockSize int) []byte {
	if blockSize%2 == 0 {
		blockSize++
	}
	half := blockSize / 2
	const c = 7 // threshold offset constant

	out := make([]byte, w*h)

	// Build integral image for fast local mean computation.
	integral := make([]int64, (w+1)*(h+1))
	iw := w + 1

	for y := 0; y < h; y++ {
		var rowSum int64
		for x := 0; x < w; x++ {
			rowSum += int64(gray[y*pitch+x])
			integral[(y+1)*iw+(x+1)] = rowSum + integral[y*iw+(x+1)]
		}
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Compute local mean using integral image.
			x0 := x - half
			y0 := y - half
			x1 := x + half + 1
			y1 := y + half + 1

			if x0 < 0 {
				x0 = 0
			}
			if y0 < 0 {
				y0 = 0
			}
			if x1 > w {
				x1 = w
			}
			if y1 > h {
				y1 = h
			}

			area := int64((x1 - x0) * (y1 - y0))
			if area == 0 {
				continue
			}

			sum := integral[y1*iw+x1] - integral[y0*iw+x1] -
				integral[y1*iw+x0] + integral[y0*iw+x0]

			mean := sum / area
			val := int64(gray[y*pitch+x])

			if val < mean-c {
				out[y*w+x] = 255
			}
		}
	}

	return out
}

// findQuads finds quadrilateral contours in a binary image using contour
// tracing and polygon approximation. Returns quads as 4 corner points,
// ordered clockwise: TL, TR, BR, BL.
func findQuads(binary []byte, w, h int) [][4][2]float64 {
	visited := make([]bool, w*h)
	var quads [][4][2]float64

	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			idx := y*w + x
			if binary[idx] == 0 || visited[idx] {
				continue
			}
			// Only start tracing at a boundary pixel: white pixel with
			// a black (or OOB) neighbor to its left.
			if x > 0 && binary[idx-1] != 0 {
				continue
			}

			contour := traceContour(binary, w, h, x, y, visited)
			if len(contour) < 16 {
				continue // too small to be a marker
			}

			// Approximate contour with Douglas-Peucker.
			// Use perimeter-based epsilon for scale invariance.
			perimeter := contourPerimeter(contour)
			epsilon := perimeter * 0.04
			approx := douglasPeucker(contour, epsilon)

			// Must be a quadrilateral.
			if len(approx) != 4 {
				continue
			}

			// Convert to float64 corners.
			var corners [4][2]float64
			for i, p := range approx {
				corners[i] = [2]float64{float64(p[0]), float64(p[1])}
			}

			// Check convexity: all cross products must have the same sign.
			if !isConvexQuad(corners) {
				continue
			}

			// Check minimum area.
			area := quadArea(corners)
			if area < 64.0 {
				continue
			}

			// Check minimum edge length.
			minEdge := 8.0
			tooShort := false
			for i := 0; i < 4; i++ {
				j := (i + 1) % 4
				dx := corners[j][0] - corners[i][0]
				dy := corners[j][1] - corners[i][1]
				if math.Sqrt(dx*dx+dy*dy) < minEdge {
					tooShort = true
					break
				}
			}
			if tooShort {
				continue
			}

			// Order corners clockwise starting from nearest to top-left of bounding box.
			ordered := orderCornersCW(corners)
			quads = append(quads, ordered)
		}
	}

	return quads
}

// findSquares is the legacy name for findQuads. It finds 4-sided contour
// structures in a binary image using Moore boundary tracing and
// Douglas-Peucker polygon approximation.
func findSquares(binary []byte, w, h int) [][4][2]float64 {
	return findQuads(binary, w, h)
}

// traceContour traces the boundary of a connected component starting at
// (startX, startY) using Moore boundary tracing (8-connected). Returns
// ordered boundary points. Also marks interior pixels as visited.
func traceContour(binary []byte, w, h, startX, startY int, visited []bool) [][2]int {
	// 8-connectivity direction offsets (clockwise from right):
	// 0=E, 1=SE, 2=S, 3=SW, 4=W, 5=NW, 6=N, 7=NE
	dx := [8]int{1, 1, 0, -1, -1, -1, 0, 1}
	dy := [8]int{0, 1, 1, 1, 0, -1, -1, -1}

	isWhite := func(x, y int) bool {
		if x < 0 || x >= w || y < 0 || y >= h {
			return false
		}
		return binary[y*w+x] != 0
	}

	var contour [][2]int
	contour = append(contour, [2]int{startX, startY})
	visited[startY*w+startX] = true

	// Start direction: we entered from the left, so the backtrack direction is West (4).
	dir := 4 // direction we came from

	curX, curY := startX, startY
	maxSteps := w * h // safety limit

	for step := 0; step < maxSteps; step++ {
		// Start searching from (dir+1) mod 8 — the next direction clockwise
		// from the direction we came from, which is the Moore neighborhood rule.
		// Actually: start from (backtrack + 1) mod 8 where backtrack = (dir+4)%8.
		searchStart := (dir + 1) % 8

		found := false
		for i := 0; i < 8; i++ {
			d := (searchStart + i) % 8
			nx := curX + dx[d]
			ny := curY + dy[d]

			if isWhite(nx, ny) {
				curX, curY = nx, ny
				dir = (d + 4) % 8 // backtrack direction

				if curX == startX && curY == startY {
					// Mark all contour pixels as visited.
					for _, p := range contour {
						visited[p[1]*w+p[0]] = true
					}
					// Also flood-mark interior so we don't re-trace.
					floodMarkVisited(binary, w, h, startX, startY, visited)
					return contour
				}

				contour = append(contour, [2]int{curX, curY})
				visited[curY*w+curX] = true
				found = true
				break
			}
		}

		if !found {
			// Isolated pixel.
			break
		}
	}

	// Mark visited to avoid re-tracing.
	for _, p := range contour {
		visited[p[1]*w+p[0]] = true
	}
	floodMarkVisited(binary, w, h, startX, startY, visited)
	return contour
}

// floodMarkVisited marks all connected white pixels starting from (sx, sy)
// as visited, to prevent re-tracing the same blob.
func floodMarkVisited(binary []byte, w, h, sx, sy int, visited []bool) {
	stack := []int{sy*w + sx}
	for len(stack) > 0 {
		idx := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if idx < 0 || idx >= w*h || visited[idx] || binary[idx] == 0 {
			continue
		}
		visited[idx] = true

		cx := idx % w
		cy := idx / w
		if cx > 0 {
			stack = append(stack, idx-1)
		}
		if cx < w-1 {
			stack = append(stack, idx+1)
		}
		if cy > 0 {
			stack = append(stack, idx-w)
		}
		if cy < h-1 {
			stack = append(stack, idx+w)
		}
	}
}

// douglasPeucker simplifies a polygon (closed contour) using the
// Douglas-Peucker algorithm. epsilon controls the approximation
// tolerance (in pixels).
func douglasPeucker(points [][2]int, epsilon float64) [][2]int {
	if len(points) <= 2 {
		return points
	}

	// For a closed contour, find the two farthest-apart points to split.
	// This gives a better starting decomposition than using the first/last.
	maxDist := 0.0
	splitA, splitB := 0, len(points)/2
	for i := 0; i < len(points); i++ {
		for j := i + 1; j < len(points); j++ {
			dx := float64(points[j][0] - points[i][0])
			dy := float64(points[j][1] - points[i][1])
			d := dx*dx + dy*dy
			if d > maxDist {
				maxDist = d
				splitA, splitB = i, j
			}
		}
	}

	// Build two chains: A→B and B→A (wrapping around).
	var chain1, chain2 [][2]int
	if splitA <= splitB {
		chain1 = points[splitA : splitB+1]
		chain2 = make([][2]int, 0, len(points)-splitB+splitA+1)
		chain2 = append(chain2, points[splitB:]...)
		chain2 = append(chain2, points[:splitA+1]...)
	} else {
		chain1 = points[splitB : splitA+1]
		chain2 = make([][2]int, 0, len(points)-splitA+splitB+1)
		chain2 = append(chain2, points[splitA:]...)
		chain2 = append(chain2, points[:splitB+1]...)
	}

	s1 := dpSimplify(chain1, epsilon)
	s2 := dpSimplify(chain2, epsilon)

	// Merge the two simplified chains (remove duplicate endpoints).
	result := make([][2]int, 0, len(s1)+len(s2)-2)
	result = append(result, s1...)
	if len(s2) > 2 {
		result = append(result, s2[1:len(s2)-1]...)
	}

	return result
}

// dpSimplify is the recursive Douglas-Peucker simplification for an open polyline.
func dpSimplify(points [][2]int, epsilon float64) [][2]int {
	if len(points) <= 2 {
		return points
	}

	// Find point farthest from line between first and last.
	first := points[0]
	last := points[len(points)-1]
	maxDist := 0.0
	maxIdx := 0

	for i := 1; i < len(points)-1; i++ {
		d := pointToLineDistance(points[i], first, last)
		if d > maxDist {
			maxDist = d
			maxIdx = i
		}
	}

	if maxDist > epsilon {
		left := dpSimplify(points[:maxIdx+1], epsilon)
		right := dpSimplify(points[maxIdx:], epsilon)
		return append(left[:len(left)-1], right...)
	}

	return [][2]int{first, last}
}

// pointToLineDistance computes the perpendicular distance from point p
// to the line defined by points a and b.
func pointToLineDistance(p, a, b [2]int) float64 {
	dx := float64(b[0] - a[0])
	dy := float64(b[1] - a[1])
	lenSq := dx*dx + dy*dy
	if lenSq == 0 {
		fx := float64(p[0] - a[0])
		fy := float64(p[1] - a[1])
		return math.Sqrt(fx*fx + fy*fy)
	}
	// |cross product| / |line length|
	cross := math.Abs(dx*float64(p[1]-a[1]) - dy*float64(p[0]-a[0]))
	return cross / math.Sqrt(lenSq)
}

// contourPerimeter computes the perimeter of a closed contour.
func contourPerimeter(contour [][2]int) float64 {
	if len(contour) < 2 {
		return 0
	}
	total := 0.0
	for i := 0; i < len(contour); i++ {
		j := (i + 1) % len(contour)
		dx := float64(contour[j][0] - contour[i][0])
		dy := float64(contour[j][1] - contour[i][1])
		total += math.Sqrt(dx*dx + dy*dy)
	}
	return total
}

// isConvexQuad checks that a quadrilateral (4 corners) is convex.
func isConvexQuad(corners [4][2]float64) bool {
	var sign float64
	for i := 0; i < 4; i++ {
		j := (i + 1) % 4
		k := (i + 2) % 4
		dx1 := corners[j][0] - corners[i][0]
		dy1 := corners[j][1] - corners[i][1]
		dx2 := corners[k][0] - corners[j][0]
		dy2 := corners[k][1] - corners[j][1]
		cross := dx1*dy2 - dy1*dx2
		if i == 0 {
			sign = cross
		} else if sign*cross < 0 {
			return false
		}
	}
	return sign != 0
}

// quadArea computes the area of a quadrilateral using the shoelace formula.
func quadArea(corners [4][2]float64) float64 {
	area := 0.0
	for i := 0; i < 4; i++ {
		j := (i + 1) % 4
		area += corners[i][0]*corners[j][1] - corners[j][0]*corners[i][1]
	}
	return math.Abs(area) / 2.0
}

// orderCornersCW orders 4 corners clockwise (in image coordinates where
// Y increases downward) starting from the one nearest top-left.
// Result order: TL, TR, BR, BL.
func orderCornersCW(corners [4][2]float64) [4][2]float64 {
	// Find centroid.
	var cx, cy float64
	for _, c := range corners {
		cx += c[0]
		cy += c[1]
	}
	cx /= 4
	cy /= 4

	// Sort by angle from centroid. In image coordinates (Y-down), increasing
	// atan2 order gives clockwise traversal: TL(-,-) → TR(+,-) → BR(+,+) → BL(-,+).
	type cornerAngle struct {
		pt    [2]float64
		angle float64
	}
	ca := make([]cornerAngle, 4)
	for i, c := range corners {
		ca[i] = cornerAngle{c, math.Atan2(c[1]-cy, c[0]-cx)}
	}
	sort.Slice(ca, func(i, j int) bool {
		return ca[i].angle < ca[j].angle
	})

	// Find the corner nearest top-left (smallest x+y) and rotate to front.
	minSum := math.MaxFloat64
	minIdx := 0
	for i, c := range ca {
		s := c.pt[0] + c.pt[1]
		if s < minSum {
			minSum = s
			minIdx = i
		}
	}

	var result [4][2]float64
	for i := 0; i < 4; i++ {
		result[i] = ca[(minIdx+i)%4].pt
	}
	return result
}

// decodeMarker attempts to read a 4x4 binary pattern from the image region
// defined by the quadrilateral corners and look it up in DICT_4X4_50.
// Uses a homography (projective transform) for perspective-correct sampling.
func decodeMarker(gray []byte, w, h, pitch int, corners [4][2]float64) (int, bool) {
	// Compute the homography from the unit square to the detected quad.
	H := computeHomography(corners)

	// Sample the inner 4x4 grid of the marker (inside the black border).
	// A standard ArUco marker has: 1-cell black border + 4x4 data cells + 1-cell black border = 6x6 total.
	cellsTotal := 6.0
	var bits [4][4]bool

	for row := 0; row < 4; row++ {
		for col := 0; col < 4; col++ {
			// Normalized position within the marker (skipping the border).
			u := (float64(col+1) + 0.5) / cellsTotal
			v := (float64(row+1) + 0.5) / cellsTotal

			// Map through homography to image coordinates.
			px, py, ok := applyHomography(H, u, v)
			if !ok {
				return 0, false
			}

			// Bounds check.
			ix := int(px + 0.5)
			iy := int(py + 0.5)
			if ix < 0 || ix >= w || iy < 0 || iy >= h {
				return 0, false
			}

			// Sample the pixel. White = 1, black = 0.
			bits[row][col] = gray[iy*pitch+ix] > 128
		}
	}

	// Look up the bit pattern (trying all 4 rotations).
	id, ok := lookupMarkerID(bits)
	return id, ok
}

// computeHomography computes the 3x3 perspective transform matrix that maps
// from the unit square [0,1]x[0,1] to the quadrilateral defined by corners.
// Corners are ordered: TL(0,0), TR(1,0), BR(1,1), BL(0,1).
// Uses the Direct Linear Transform (DLT) method: each point correspondence
// gives 2 equations, 4 points = 8 equations for 8 unknowns (h33 = 1).
func computeHomography(corners [4][2]float64) [9]float64 {
	// Source points (unit square corners matching TL, TR, BR, BL).
	src := [4][2]float64{
		{0, 0}, // TL
		{1, 0}, // TR
		{1, 1}, // BR
		{0, 1}, // BL
	}

	// Build the 8x8 linear system Ah = b where h = [h11,h12,h13,h21,h22,h23,h31,h32].
	// For each correspondence (x,y) -> (X,Y):
	//   x*h11 + y*h12 + h13 - x*X*h31 - y*X*h32 = X
	//   x*h21 + y*h22 + h23 - x*Y*h31 - y*Y*h32 = Y
	var A [8][8]float64
	var b [8]float64

	for i := 0; i < 4; i++ {
		sx, sy := src[i][0], src[i][1]
		dx, dy := corners[i][0], corners[i][1]
		r := i * 2

		A[r][0] = sx
		A[r][1] = sy
		A[r][2] = 1
		A[r][3] = 0
		A[r][4] = 0
		A[r][5] = 0
		A[r][6] = -sx * dx
		A[r][7] = -sy * dx
		b[r] = dx

		A[r+1][0] = 0
		A[r+1][1] = 0
		A[r+1][2] = 0
		A[r+1][3] = sx
		A[r+1][4] = sy
		A[r+1][5] = 1
		A[r+1][6] = -sx * dy
		A[r+1][7] = -sy * dy
		b[r+1] = dy
	}

	// Solve with Gaussian elimination with partial pivoting.
	h := solveLinear8x8(A, b)

	return [9]float64{
		h[0], h[1], h[2],
		h[3], h[4], h[5],
		h[6], h[7], 1.0,
	}
}

// applyHomography maps a point (u, v) through the 3x3 homography H.
// Returns the projected (x, y) and whether the projection is valid (w != 0).
func applyHomography(H [9]float64, u, v float64) (float64, float64, bool) {
	x := H[0]*u + H[1]*v + H[2]
	y := H[3]*u + H[4]*v + H[5]
	w := H[6]*u + H[7]*v + H[8]
	if math.Abs(w) < 1e-10 {
		return 0, 0, false
	}
	return x / w, y / w, true
}

// solveLinear8x8 solves an 8x8 linear system Ax = b using Gaussian elimination
// with partial pivoting.
func solveLinear8x8(A [8][8]float64, b [8]float64) [8]float64 {
	const n = 8
	// Augmented matrix.
	var aug [n][n + 1]float64
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			aug[i][j] = A[i][j]
		}
		aug[i][n] = b[i]
	}

	// Forward elimination with partial pivoting.
	for col := 0; col < n; col++ {
		// Find pivot.
		maxVal := math.Abs(aug[col][col])
		maxRow := col
		for row := col + 1; row < n; row++ {
			if v := math.Abs(aug[row][col]); v > maxVal {
				maxVal = v
				maxRow = row
			}
		}
		// Swap rows.
		if maxRow != col {
			aug[col], aug[maxRow] = aug[maxRow], aug[col]
		}

		pivot := aug[col][col]
		if math.Abs(pivot) < 1e-12 {
			continue // singular or near-singular
		}

		// Eliminate below.
		for row := col + 1; row < n; row++ {
			factor := aug[row][col] / pivot
			for j := col; j <= n; j++ {
				aug[row][j] -= factor * aug[col][j]
			}
		}
	}

	// Back substitution.
	var x [n]float64
	for i := n - 1; i >= 0; i-- {
		sum := aug[i][n]
		for j := i + 1; j < n; j++ {
			sum -= aug[i][j] * x[j]
		}
		if math.Abs(aug[i][i]) < 1e-12 {
			x[i] = 0
		} else {
			x[i] = sum / aug[i][i]
		}
	}

	return x
}

// interpolateQuad performs bilinear interpolation within a quadrilateral.
// u, v are in [0, 1] representing position within the quad.
// Kept for backward compatibility; decodeMarker now uses homography instead.
func interpolateQuad(corners [4][2]float64, u, v float64) (float64, float64) {
	// Bilinear interpolation: corners ordered TL, TR, BR, BL.
	// P = (1-v)*((1-u)*TL + u*TR) + v*((1-u)*BL + u*BR)
	tl := corners[0]
	tr := corners[1]
	br := corners[2]
	bl := corners[3]

	top := [2]float64{
		(1-u)*tl[0] + u*tr[0],
		(1-u)*tl[1] + u*tr[1],
	}
	bot := [2]float64{
		(1-u)*bl[0] + u*br[0],
		(1-u)*bl[1] + u*br[1],
	}

	return (1-v)*top[0] + v*bot[0], (1-v)*top[1] + v*bot[1]
}

// lookupMarkerID searches for a 4x4 bit pattern in DICT_4X4_50.
// It checks all 4 rotations and returns the marker ID if found.
func lookupMarkerID(bits [4][4]bool) (int, bool) {
	// Check all 4 rotations.
	rotated := bits
	for r := 0; r < 4; r++ {
		for id, pattern := range dict4x4_50 {
			if rotated == pattern {
				return id, true
			}
		}
		rotated = rotateBits90(rotated)
	}
	return 0, false
}

// rotateBits90 rotates a 4x4 bit grid 90 degrees clockwise.
func rotateBits90(bits [4][4]bool) [4][4]bool {
	var result [4][4]bool
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			result[c][3-r] = bits[r][c]
		}
	}
	return result
}

// dict4x4_50 is the first 50 entries of the ArUco DICT_4X4_50 dictionary.
// Each entry is a 4x4 binary grid where true = white, false = black.
// These patterns are from the standard OpenCV ArUco dictionary.
var dict4x4_50 = [50][4][4]bool{
	// Marker 0
	{
		{true, false, false, false},
		{true, false, true, true},
		{false, false, false, true},
		{false, false, true, true},
	},
	// Marker 1
	{
		{true, false, true, true},
		{false, true, false, true},
		{false, true, true, false},
		{true, false, false, false},
	},
	// Marker 2
	{
		{false, true, false, false},
		{false, true, true, true},
		{true, false, false, true},
		{false, false, true, false},
	},
	// Marker 3
	{
		{true, true, false, true},
		{false, false, false, false},
		{false, true, true, false},
		{true, false, false, true},
	},
	// Marker 4
	{
		{false, false, true, false},
		{true, true, true, true},
		{true, false, true, false},
		{false, true, false, false},
	},
	// Marker 5
	{
		{true, true, true, false},
		{false, false, true, false},
		{true, false, false, false},
		{true, true, false, true},
	},
	// Marker 6
	{
		{false, true, true, false},
		{true, false, true, false},
		{true, true, false, true},
		{false, true, true, true},
	},
	// Marker 7
	{
		{false, false, false, true},
		{true, true, false, true},
		{false, true, false, false},
		{true, true, true, false},
	},
	// Marker 8
	{
		{true, false, false, true},
		{true, false, false, false},
		{false, true, true, false},
		{false, false, true, true},
	},
	// Marker 9
	{
		{false, true, false, true},
		{false, false, true, true},
		{true, true, false, false},
		{true, false, true, false},
	},
	// Marker 10
	{
		{true, true, false, false},
		{true, true, true, false},
		{false, false, true, true},
		{true, false, false, false},
	},
	// Marker 11
	{
		{false, false, true, true},
		{false, true, false, false},
		{true, true, false, true},
		{false, true, false, true},
	},
	// Marker 12
	{
		{true, true, true, true},
		{false, false, false, true},
		{true, true, true, false},
		{false, true, true, false},
	},
	// Marker 13
	{
		{false, true, true, true},
		{true, false, false, false},
		{false, false, true, true},
		{true, true, false, false},
	},
	// Marker 14
	{
		{true, false, true, false},
		{false, true, true, false},
		{true, false, false, true},
		{false, true, false, true},
	},
	// Marker 15
	{
		{false, false, false, false},
		{true, true, false, false},
		{false, false, false, false},
		{true, true, true, true},
	},
	// Marker 16
	{
		{true, false, true, true},
		{true, true, false, false},
		{false, false, true, false},
		{false, true, true, false},
	},
	// Marker 17
	{
		{false, true, false, true},
		{true, true, true, false},
		{false, true, false, true},
		{true, true, false, true},
	},
	// Marker 18
	{
		{false, false, false, true},
		{false, false, false, true},
		{true, true, true, true},
		{true, false, false, true},
	},
	// Marker 19
	{
		{true, true, false, false},
		{false, true, false, true},
		{true, false, true, true},
		{false, false, false, true},
	},
	// Marker 20
	{
		{true, false, false, false},
		{false, true, true, false},
		{false, true, false, false},
		{true, false, false, false},
	},
	// Marker 21
	{
		{false, true, true, false},
		{false, false, false, true},
		{true, false, true, true},
		{false, false, false, false},
	},
	// Marker 22
	{
		{false, false, true, true},
		{true, false, true, true},
		{false, true, false, false},
		{true, true, true, false},
	},
	// Marker 23
	{
		{true, true, true, false},
		{true, true, false, true},
		{true, true, true, false},
		{false, false, true, false},
	},
	// Marker 24
	{
		{true, true, false, true},
		{true, false, true, false},
		{false, false, false, true},
		{true, true, false, false},
	},
	// Marker 25
	{
		{false, false, false, false},
		{false, true, true, true},
		{true, true, false, false},
		{false, true, true, true},
	},
	// Marker 26
	{
		{false, true, false, false},
		{true, false, false, true},
		{false, false, true, false},
		{true, false, true, true},
	},
	// Marker 27
	{
		{true, false, true, false},
		{true, true, false, true},
		{false, true, true, true},
		{true, true, true, true},
	},
	// Marker 28
	{
		{true, true, true, true},
		{true, false, false, true},
		{false, false, false, false},
		{false, false, false, true},
	},
	// Marker 29
	{
		{false, false, true, false},
		{false, true, false, false},
		{true, false, false, false},
		{false, true, false, false},
	},
	// Marker 30
	{
		{false, true, false, true},
		{false, false, false, false},
		{false, true, true, true},
		{false, false, true, true},
	},
	// Marker 31
	{
		{true, false, false, true},
		{false, true, true, false},
		{true, true, false, false},
		{true, false, true, true},
	},
	// Marker 32
	{
		{true, true, false, false},
		{true, false, true, true},
		{true, false, false, false},
		{false, true, false, true},
	},
	// Marker 33
	{
		{false, false, false, false},
		{true, false, false, false},
		{false, false, true, true},
		{false, false, false, false},
	},
	// Marker 34
	{
		{false, true, true, true},
		{false, true, false, true},
		{true, true, false, false},
		{false, true, false, false},
	},
	// Marker 35
	{
		{true, false, false, true},
		{true, true, true, true},
		{true, false, true, true},
		{false, false, true, false},
	},
	// Marker 36
	{
		{false, false, true, false},
		{false, false, true, true},
		{false, true, true, false},
		{true, true, false, true},
	},
	// Marker 37
	{
		{true, true, true, true},
		{false, true, true, false},
		{true, false, false, true},
		{true, false, false, false},
	},
	// Marker 38
	{
		{true, false, true, false},
		{false, false, false, false},
		{true, true, true, false},
		{false, true, true, false},
	},
	// Marker 39
	{
		{false, true, false, false},
		{true, true, true, false},
		{false, false, false, true},
		{false, false, true, true},
	},
	// Marker 40
	{
		{true, true, false, true},
		{false, true, false, false},
		{true, false, true, false},
		{false, false, false, false},
	},
	// Marker 41
	{
		{false, false, false, true},
		{false, true, true, false},
		{false, true, true, true},
		{true, true, false, true},
	},
	// Marker 42
	{
		{false, true, true, false},
		{true, true, false, false},
		{false, false, true, true},
		{false, true, false, true},
	},
	// Marker 43
	{
		{true, false, false, false},
		{true, false, true, false},
		{true, true, true, true},
		{true, true, false, false},
	},
	// Marker 44
	{
		{false, false, true, true},
		{false, false, false, false},
		{true, false, true, false},
		{false, true, true, true},
	},
	// Marker 45
	{
		{true, true, false, false},
		{false, false, false, true},
		{false, true, false, true},
		{true, true, true, false},
	},
	// Marker 46
	{
		{false, true, false, true},
		{true, false, true, false},
		{true, false, false, false},
		{true, false, true, false},
	},
	// Marker 47
	{
		{true, false, true, true},
		{false, false, true, false},
		{true, true, false, true},
		{true, false, false, true},
	},
	// Marker 48
	{
		{false, false, false, false},
		{false, false, true, false},
		{true, true, false, true},
		{true, true, true, false},
	},
	// Marker 49
	{
		{true, true, true, false},
		{true, false, false, false},
		{false, true, false, false},
		{false, false, false, true},
	},
}
