//go:build arm64

package graphics

// alphaBlendRGBA10YRow blends 8-bit RGBA overlay onto 10-bit Y plane (uint16 LE).
// ARM64 NEON implementation.
//
//go:noescape
func alphaBlendRGBA10YRow(yPlane *byte, rgba *byte, count int)

// alphaBlendRGBA10UVRow blends 8-bit RGBA overlay onto 10-bit Cb/Cr planes (uint16 LE).
// For each chroma sample, uses the left (even) RGBA pixel of the pair.
// ARM64 NEON implementation.
//
//go:noescape
func alphaBlendRGBA10UVRow(cbPlane, crPlane *byte, rgba *byte, chromaCount int)
