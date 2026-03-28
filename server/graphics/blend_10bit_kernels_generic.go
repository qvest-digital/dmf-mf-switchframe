//go:build !amd64 && !arm64

package graphics

// alphaBlendRGBA10YRow blends 8-bit RGBA overlay onto 10-bit Y plane (uint16 LE).
// Generic Go fallback.
func alphaBlendRGBA10YRow(yPlane *byte, rgba *byte, count int) {
	alphaBlendRGBA10YRowGo(yPlane, rgba, count)
}

// alphaBlendRGBA10UVRow blends 8-bit RGBA overlay onto 10-bit Cb/Cr planes (uint16 LE).
// Generic Go fallback.
func alphaBlendRGBA10UVRow(cbPlane, crPlane *byte, rgba *byte, chromaCount int) {
	alphaBlendRGBA10UVRowGo(cbPlane, crPlane, rgba, chromaCount)
}
