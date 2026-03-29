package tracking

import "math"

// superpointDescDim is the dimensionality of SuperPoint descriptors.
const superpointDescDim = 256

// descriptorMatch represents a match between a query and train descriptor.
type descriptorMatch struct {
	queryIdx int     // index in the frame keypoints
	trainIdx int     // index in the studio map points
	distance float32 // squared L2 distance
}

// matchDescriptors performs brute-force nearest-neighbor matching between
// frame descriptors and map descriptors, using Lowe's ratio test to filter
// ambiguous matches.
func matchDescriptors(frameDescs []float32, mapDescs [][]float32, numFrame int, ratioThresh float64) []descriptorMatch {
	var matches []descriptorMatch

	for i := 0; i < numFrame; i++ {
		queryDesc := frameDescs[i*superpointDescDim : (i+1)*superpointDescDim]

		bestDist := float32(math.MaxFloat32)
		secondDist := float32(math.MaxFloat32)
		bestIdx := -1

		for j, mapDesc := range mapDescs {
			dist := l2DistanceSquared(queryDesc, mapDesc)
			if dist < bestDist {
				secondDist = bestDist
				bestDist = dist
				bestIdx = j
			} else if dist < secondDist {
				secondDist = dist
			}
		}

		// Lowe's ratio test: compare sqrt of squared distances.
		if bestIdx >= 0 && secondDist > 0 {
			ratio := math.Sqrt(float64(bestDist) / float64(secondDist))
			if ratio < ratioThresh {
				matches = append(matches, descriptorMatch{
					queryIdx: i,
					trainIdx: bestIdx,
					distance: bestDist,
				})
			}
		}
	}

	return matches
}

// l2DistanceSquared computes the squared L2 distance between two float32 vectors.
func l2DistanceSquared(a, b []float32) float32 {
	var sum float32
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum
}
